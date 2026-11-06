package deploy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"

	"github.com/behaviorengineering/polypus/internal/deploy/execx"
	"github.com/behaviorengineering/polypus/internal/deploy/smoke"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// composeArgsPrefix is how many argv entries precede the compose subcommand:
// docker compose -f F -p P --env-file A --env-file B.
const composeArgsPrefix = 10

// fakeDocker records every docker command a deploy runs and fails the ones whose
// compose subcommand starts with a scripted prefix.
type fakeDocker struct {
	mu    sync.Mutex
	calls []string
	fail  map[string]error
}

func (d *fakeDocker) record(args []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	sub := ""
	if len(args) > composeArgsPrefix-1 {
		sub = strings.Join(args[composeArgsPrefix-1:], " ")
	}
	d.calls = append(d.calls, sub)
	for prefix, err := range d.fail {
		if strings.HasPrefix(sub, prefix) {
			return err
		}
	}
	return nil
}

func (d *fakeDocker) run(_ context.Context, _ string, args, _ []string) error {
	return d.record(args)
}

func (d *fakeDocker) output(_ context.Context, _ string, args, _ []string) ([]byte, error) {
	if err := d.record(args); err != nil {
		return nil, err
	}
	return []byte("fake docker output"), nil
}

func (d *fakeDocker) subcommands() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.calls)
}

// gatewayStub answers the health endpoints with healthStatus and the admin allow
// list with adminStatus.
func gatewayStub(t *testing.T, healthStatus, adminStatus int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health", "/health/backends":
			w.WriteHeader(healthStatus)
		case "/v1/admin/models/allow":
			w.WriteHeader(adminStatus)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// applyFixture is one environment deployed against fakes: no Docker daemon, no
// registry, no keyring and no network beyond the httptest gateway.
type applyFixture struct {
	opts   Options
	docker *fakeDocker
	out    *strings.Builder
	smoked *int
}

func newApplyFixture(t *testing.T, gatewayURL string) applyFixture {
	t.Helper()
	env := t.TempDir()
	gateway := filepath.Join(env, "gateway.yaml")
	pins := filepath.Join(env, "images.env")
	files := map[string]string{
		gateway: "backends:\n  openrouter:\n    base_url: https://openrouter.ai/api/v1\n    auth:\n      bearer_env: OPENROUTER_API_KEY\n    capabilities: [chat]\n    models:\n      allow: [openai/gpt-oss-20b]\n",
		pins:    "POLYPUS_IMAGE=ghcr.io/behaviorengineering/polypus:0.3.0\nTAILSCALE_IMAGE=tailscale/tailscale:v1.102.4\n",
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The process environment wins over the keyring, so the secrets come from here.
	t.Setenv("TS_AUTHKEY", "tskey-auth-"+strings.Repeat("t", 24))
	t.Setenv("OPENROUTER_API_KEY", "sk-or-"+strings.Repeat("k", 24))
	t.Setenv("POLYPUS_API_KEY", "")
	t.Setenv("SOPS_SECRETS_FILE", "")
	t.Setenv("POLYPUS_SMOKE_PROVIDERS", "")
	t.Setenv("POLYPUS_SMOKE_SYSTEMONE", "")

	docker := &fakeDocker{fail: map[string]error{}}
	var out strings.Builder
	smoked := 0
	opts := DefaultOptions(filepath.Join(t.TempDir(), "state"))
	opts.Name = "polypus-test"
	opts.GatewayConfig = gateway
	opts.PinsFile = pins
	opts.HealthURL = gatewayURL
	opts.HealthTimeout = 2 * time.Second
	opts.HealthPollInterval = time.Millisecond
	opts.Keyring = operatorconfig.NewMemKeyring()
	opts.SkipOverlayPrune = true
	opts.DockerRun = docker.run
	opts.DockerOutput = docker.output
	opts.DockerLookPath = func(string) (string, error) { return "/usr/bin/docker", nil }
	opts.ImageProbe = func(context.Context, string, string) error { return nil }
	opts.Smoke = func(context.Context, smoke.Options) error {
		smoked++
		return nil
	}
	opts.Out = &out
	return applyFixture{opts: opts, docker: docker, out: &out, smoked: &smoked}
}

func (f applyFixture) run(t *testing.T) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return Run(ctx, f.opts)
}

// diagnosticsRan reports whether the failure dump asked docker for the gateway's
// logs and state.
func (f applyFixture) diagnosticsRan() bool {
	calls := f.docker.subcommands()
	return slices.Contains(calls, "logs --no-color --tail 120 gateway") && slices.ContainsFunc(calls, func(c string) bool {
		return strings.HasPrefix(c, "ps --all")
	})
}

func TestApplyHappyPath(t *testing.T) {
	f := newApplyFixture(t, gatewayStub(t, http.StatusOK, http.StatusUnauthorized).URL)
	if err := f.run(t); err != nil {
		t.Fatalf("deploy: %v\n%s", err, f.out)
	}
	want := []string{
		"pull",
		"up -d",
		"rm --stop --force switchyard",
		"rm --stop --force otelcol phoenix hyperdx",
		"up -d --force-recreate --no-deps gateway tailscale",
	}
	if got := f.docker.subcommands(); !slices.Equal(got, want) {
		t.Fatalf("docker subcommands\n got %q\nwant %q", got, want)
	}
	if *f.smoked != 1 {
		t.Fatalf("smoke ran %d times, want 1", *f.smoked)
	}
	if !strings.Contains(f.out.String(), "deploy ok: "+f.opts.HealthURL+"/health (smoke passed)") {
		t.Fatalf("missing the ok line:\n%s", f.out)
	}
	if strings.Contains(f.out.String(), "no access keys") {
		t.Fatalf("a locked gateway gets no keyless hint:\n%s", f.out)
	}
}

func TestApplyComposeUpFailsCollectsDiagnostics(t *testing.T) {
	f := newApplyFixture(t, gatewayStub(t, http.StatusOK, http.StatusOK).URL)
	upErr := &execx.RunError{Name: "docker", Stderr: "service gateway failed to start", Err: errors.New("exit status 1")}
	f.docker.fail["up -d"] = upErr
	err := f.run(t)
	if !errors.Is(err, upErr) {
		t.Fatalf("err = %v, want it to wrap the compose up failure", err)
	}
	if !f.diagnosticsRan() {
		t.Fatalf("diagnostics did not run through the fake docker: %q", f.docker.subcommands())
	}
	if *f.smoked != 0 {
		t.Fatal("smoke must not run after a failed compose up")
	}
}

func TestApplyHealthTimeoutCollectsDiagnostics(t *testing.T) {
	f := newApplyFixture(t, gatewayStub(t, http.StatusServiceUnavailable, http.StatusOK).URL)
	f.opts.HealthTimeout = 200 * time.Millisecond
	err := f.run(t)
	if derrors.CodeOf(err) != derrors.CodeTimeout {
		t.Fatalf("err = %v, want CodeTimeout", err)
	}
	if !f.diagnosticsRan() {
		t.Fatalf("diagnostics did not run: %q", f.docker.subcommands())
	}
	if !strings.Contains(f.out.String(), "gateway diagnostics") || !strings.Contains(f.out.String(), "fake docker output") {
		t.Fatalf("diagnostics not printed:\n%s", f.out)
	}
	if *f.smoked != 0 {
		t.Fatal("smoke must not run before the gateway is healthy")
	}
}

func TestApplySmokeFailsCollectsDiagnostics(t *testing.T) {
	f := newApplyFixture(t, gatewayStub(t, http.StatusOK, http.StatusUnauthorized).URL)
	smokeErr := errors.New("smoke openrouter/openai/gpt-oss-20b: empty content")
	f.opts.Smoke = func(context.Context, smoke.Options) error { return smokeErr }
	err := f.run(t)
	if !errors.Is(err, smokeErr) {
		t.Fatalf("err = %v, want the smoke failure", err)
	}
	if !f.diagnosticsRan() {
		t.Fatalf("diagnostics did not run: %q", f.docker.subcommands())
	}
	if strings.Contains(f.out.String(), "deploy ok") {
		t.Fatalf("a failed smoke must not print deploy ok:\n%s", f.out)
	}
}

func TestApplySkipSmoke(t *testing.T) {
	f := newApplyFixture(t, gatewayStub(t, http.StatusOK, http.StatusUnauthorized).URL)
	f.opts.SkipSmoke = true
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	if *f.smoked != 0 {
		t.Fatal("--skip-smoke must not run smoke")
	}
	if !strings.Contains(f.out.String(), "deploy: skip smoke (--skip-smoke)") {
		t.Fatalf("missing the skip line:\n%s", f.out)
	}
}

func TestDeployRunHintsKeyless(t *testing.T) {
	f := newApplyFixture(t, gatewayStub(t, http.StatusOK, http.StatusOK).URL)
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.out.String(), keylessHint) {
		t.Fatalf("a gateway with an empty key store must get the hint:\n%s", f.out)
	}
}
