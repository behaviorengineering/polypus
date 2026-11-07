package overlay

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/deploy/compose"
	"github.com/behaviorengineering/polypus/internal/deploy/execx"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// fakeExec answers the overlay's docker compose exec calls: cat returns catOut
// (or catErr), and the write records its stdin.
type fakeExec struct {
	catOut  string
	catErr  error
	writes  [][]byte
	writeIn []string
}

func (f *fakeExec) stdout(_ context.Context, _ string, args, _ []string, stdin []byte) ([]byte, error) {
	i := slices.Index(args, "-T")
	cmd := args[i+2:]
	if cmd[0] == "cat" {
		return []byte(f.catOut), f.catErr
	}
	f.writes = append(f.writes, stdin)
	f.writeIn = cmd
	return nil, nil
}

func gatewayYAML(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	body := "backends:\n  deepinfra:\n    models:\n      allow: [ibm-granite/granite-4.2-8b]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func containerConfig(f *fakeExec, out *bytes.Buffer) ContainerConfig {
	return ContainerConfig{
		Compose: compose.Client{ComposeFile: "c.yml", Project: "polypus-work", Stdout: f.stdout, Policies: outbound.NewPolicies()},
		Out:     out,
	}
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestPruneContainerWritesThePrunedOverlayInOneExec(t *testing.T) {
	f := &fakeExec{catOut: "backends:\n  deepinfra:\n    allow: [ibm-granite/granite-4.2-8b]\n  openrouter:\n    allow: [meta-llama/llama-3.3-70b-instruct]\n"}
	var out bytes.Buffer
	if err := PruneContainer(testCtx(t), gatewayYAML(t), containerConfig(f, &out)); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != 1 {
		t.Fatalf("writes = %d, want one", len(f.writes))
	}
	if w := string(f.writes[0]); strings.Contains(w, "granite") || !strings.Contains(w, "llama-3.3") {
		t.Fatalf("written overlay %q, want granite pruned and llama kept", w)
	}
	if want := []string{"sh", "-c", writeScript, "sh", DefaultOverlayPath}; !slices.Equal(f.writeIn, want) {
		t.Fatalf("write command %q, want %q", f.writeIn, want)
	}
	if !strings.Contains(out.String(), "removed backend=deepinfra") {
		t.Fatalf("output %q, want the removal reported", out.String())
	}
}

func TestPruneContainerMissingOverlayIsANoOp(t *testing.T) {
	f := &fakeExec{catErr: &execx.RunError{Name: "docker", Stderr: "cat: /var/lib/polypus/state/x: No such file or directory", Err: errors.New("exit status 1")}}
	var out bytes.Buffer
	if err := PruneContainer(testCtx(t), gatewayYAML(t), containerConfig(f, &out)); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != 0 || !strings.Contains(out.String(), "no-op") {
		t.Fatalf("writes %d, output %q; want a no-op", len(f.writes), out.String())
	}
}

func TestPruneContainerReadFailureNamesTheStep(t *testing.T) {
	f := &fakeExec{catErr: &execx.RunError{Name: "docker", Stderr: "service \"gateway\" is not running", Err: errors.New("exit status 1")}}
	err := PruneContainer(testCtx(t), gatewayYAML(t), containerConfig(f, &bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), "overlay.read") || !strings.Contains(err.Error(), "is not running") {
		t.Fatalf("err = %v, want the read step and docker's stderr", err)
	}
}
