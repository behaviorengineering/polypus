package deploy

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"

	"github.com/behaviorengineering/polypus/internal/clients/github"
	"github.com/behaviorengineering/polypus/internal/deploy/secrets"
	"github.com/behaviorengineering/polypus/internal/deploy/smoke"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// TestApplyPrunesTheOverlayThroughTheComposeClient checks the overlay prune runs
// through Options.DockerStdout like every other docker call, reading the overlay
// and writing the pruned one back on stdin.
func TestApplyPrunesTheOverlayThroughTheComposeClient(t *testing.T) {
	f := newApplyFixture(t, gatewayStub(t, http.StatusOK, http.StatusUnauthorized).URL)
	f.opts.SkipOverlayPrune = false
	var written []byte
	f.opts.DockerStdout = func(_ context.Context, _ string, args, _ []string, stdin []byte) ([]byte, error) {
		if slices.Contains(args, "cat") {
			return []byte("backends:\n  openrouter:\n    allow: [openai/gpt-oss-20b, extra/model]\n"), nil
		}
		written = stdin
		return nil, nil
	}
	if err := f.run(t); err != nil {
		t.Fatalf("deploy: %v\n%s", err, f.out)
	}
	if !strings.Contains(string(written), "extra/model") || strings.Contains(string(written), "gpt-oss-20b") {
		t.Fatalf("written overlay %q, want the YAML's own model pruned", written)
	}
}

// TestApplyImageFailureIsOnePhase checks images.ensure runs beside prepare, not
// inside it, so a missing image logs one phase failure.
func TestApplyImageFailureIsOnePhase(t *testing.T) {
	f := newApplyFixture(t, gatewayStub(t, http.StatusOK, http.StatusUnauthorized).URL)
	refused := derrors.New(derrors.CodeUnauthorized, "registry", "refused")
	f.opts.ImageProbe = func(context.Context, string) error { return refused }
	var logs bytes.Buffer
	f.opts.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	err := f.run(t)
	if !errors.Is(err, refused) || strings.Contains(err.Error(), "prepare failed") {
		t.Fatalf("err = %v, want the image failure outside prepare", err)
	}
	if n := strings.Count(logs.String(), "deploy phase failed"); n != 1 {
		t.Fatalf("logged %d phase failures, want 1:\n%s", n, logs.String())
	}
	if len(f.docker.subcommands()) != 0 {
		t.Fatalf("docker ran after the image check failed: %q", f.docker.subcommands())
	}
}

func TestApplyRefusesUnsetBudgets(t *testing.T) {
	for name, zero := range map[string]func(*Options){
		"image wait": func(o *Options) { o.ImageWaitTimeout = 0 },
		"smoke":      func(o *Options) { o.SmokeTimeout = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			f := newApplyFixture(t, gatewayStub(t, http.StatusOK, http.StatusUnauthorized).URL)
			zero(&f.opts)
			if err := f.run(t); derrors.CodeOf(err) != derrors.CodeInvalid {
				t.Fatalf("err = %v, want an unset budget refused", err)
			}
		})
	}
	f := newApplyFixture(t, gatewayStub(t, http.StatusOK, http.StatusUnauthorized).URL)
	f.opts.SmokeTimeout = 0
	f.opts.SkipSmoke = true
	if err := f.run(t); err != nil {
		t.Fatalf("a skipped smoke needs no budget: %v", err)
	}
}

// TestApplySecretConflictStopsBeforeContainers checks a shell secret that
// differs from the environment's keyring stops the deploy with a *Conflict,
// before any container changes.
func TestApplySecretConflictStopsBeforeContainers(t *testing.T) {
	f := newApplyFixture(t, gatewayStub(t, http.StatusOK, http.StatusUnauthorized).URL)
	kr := operatorconfig.NewMemKeyring()
	if err := kr.Set("polypus-test", "OPENROUTER_API_KEY", "sk-or-"+strings.Repeat("w", 24)); err != nil {
		t.Fatal(err)
	}
	f.opts.Secrets = &secrets.Resolver{Keyring: kr}
	err := f.run(t)
	var c *secrets.Conflict
	if !errors.As(err, &c) || c.Name != "OPENROUTER_API_KEY" {
		t.Fatalf("err = %v, want a *secrets.Conflict", err)
	}
	if len(f.docker.subcommands()) != 0 {
		t.Fatalf("docker ran despite the conflict: %q", f.docker.subcommands())
	}
}

func TestApplySmokeSendsTheKeyringClientKey(t *testing.T) {
	f := newApplyFixture(t, gatewayStub(t, http.StatusOK, http.StatusUnauthorized).URL)
	kr := operatorconfig.NewMemKeyring()
	if err := kr.Set("polypus-test", secrets.ClientKeyName, "pk_env"); err != nil {
		t.Fatal(err)
	}
	f.opts.Secrets = &secrets.Resolver{Keyring: kr}
	var got string
	f.opts.Smoke = func(_ context.Context, o smoke.Options) error {
		got = o.APIKey
		return nil
	}
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	if got != "pk_env" {
		t.Fatalf("smoke key %q, want the environment's keyring key", got)
	}
}

// publishedGitHub is a GitHub API that says release 0.3.0 published.
func publishedGitHub(t *testing.T) *github.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/git/ref/tags/v0.3.0"):
			_, _ = w.Write([]byte(`{"object":{"sha":"abc123"}}`))
		case strings.Contains(r.URL.Path, "/actions/runs/9/jobs"):
			_, _ = w.Write([]byte(`{"jobs":[{"name":"verify","conclusion":"success"}]}`))
		case strings.Contains(r.URL.Path, "/actions/runs"):
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":9,"name":"Auto patch release","conclusion":"success","created_at":"2026-01-01T00:00:00Z"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	gh, err := github.Config{HTTP: srv.Client(), BaseURL: srv.URL, Token: "test", OwnerRepo: "acme/polypus"}.CreateClient()
	if err != nil {
		t.Fatal(err)
	}
	return gh
}

func TestVerifyImagesThroughTheSeams(t *testing.T) {
	f := newApplyFixture(t, "http://unused")
	var probed []string
	f.opts.ImageProbe = func(_ context.Context, ref string) error {
		probed = append(probed, ref)
		return nil
	}
	f.opts.GitHub = publishedGitHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := VerifyImages(ctx, f.opts); err != nil {
		t.Fatal(err)
	}
	if want := []string{"ghcr.io/behaviorengineering/polypus:0.3.0"}; !slices.Equal(probed, want) {
		t.Fatalf("probed %q want %q", probed, want)
	}
}

func TestVerifyImagesNamesTheStepAndEnvironment(t *testing.T) {
	f := newApplyFixture(t, "http://unused")
	f.opts.ImageProbe = func(context.Context, string) error { return errors.New("registry down") }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := VerifyImages(ctx, f.opts)
	var de *derrors.Error
	if !errors.As(err, &de) || de.Fields()["environment"] != "polypus-test" || !strings.Contains(err.Error(), "registry manifests") {
		t.Fatalf("err = %v, want the step and environment named", err)
	}
}

func boolPtr(b bool) *bool { return &b }

func TestResolveSmokeFillsUnsetChecksFromTheEnvironment(t *testing.T) {
	f := newApplyFixture(t, "https://polypus-test.example.ts.net/")
	kr := operatorconfig.NewMemKeyring()
	if err := kr.Set("polypus-test", secrets.ClientKeyName, "pk_test"); err != nil {
		t.Fatal(err)
	}
	f.opts.Secrets = &secrets.Resolver{Keyring: kr}
	o, err := ResolveSmoke(SmokeRequest{Deploy: &f.opts, WithRouter: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if !o.WithProviders || !o.WithRouter || o.WithSystemOne {
		t.Fatalf("checks %+v, want providers from the environment and the router as asked", o)
	}
	if o.APIKey != "pk_test" || o.BaseURL != "https://polypus-test.example.ts.net" || o.EnvDeploy != f.opts.EnvDeployPath() || o.GatewayConfig == "" {
		t.Fatalf("options %+v", o)
	}
	o, err = ResolveSmoke(SmokeRequest{Deploy: &f.opts, ChatModel: "work/x"})
	if err != nil || o.WithProviders {
		t.Fatalf("an explicit chat model keeps the direct chat: %+v, %v", o, err)
	}
}

func TestResolveSmokeWithoutAnEnvironment(t *testing.T) {
	missing := errors.New("no environment at ~/.config")
	if _, err := ResolveSmoke(SmokeRequest{NoEnvironment: missing, WithProviders: boolPtr(true), BaseURL: "http://gw"}); !errors.Is(err, missing) {
		t.Fatalf("err = %v, want the discovery error for a check that needs an environment", err)
	}
	o, err := ResolveSmoke(SmokeRequest{NoEnvironment: missing, BaseURL: "http://gw/", ChatModel: "m"})
	if err != nil || o.BaseURL != "http://gw" || o.APIKey != "" {
		t.Fatalf("a direct chat needs no environment: %+v, %v", o, err)
	}
}
