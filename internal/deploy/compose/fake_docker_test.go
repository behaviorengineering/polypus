package compose

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/deploy/execx"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// scriptedDocker is a fake docker: each call records its argv and returns the
// next scripted error (nil once the script runs out).
type scriptedDocker struct {
	calls  [][]string
	script []error
}

func (d *scriptedDocker) run(_ context.Context, name string, args, _ []string) error {
	d.calls = append(d.calls, append([]string{name}, args...))
	if len(d.script) == 0 {
		return nil
	}
	err := d.script[0]
	d.script = d.script[1:]
	return err
}

// dockerFailure is a docker run that exited 1 having written stderr.
func dockerFailure(stderr string) error {
	return &execx.RunError{Name: "docker", Stderr: stderr, Err: errors.New("exit status 1")}
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func foundDocker(string) (string, error) { return "/usr/bin/docker", nil }

func TestPullDockerMissing(t *testing.T) {
	d := &scriptedDocker{}
	c := Client{
		ComposeFile: "f.yml",
		Run:         d.run,
		LookPath:    func(string) (string, error) { return "", exec.ErrNotFound },
		Policies:    outbound.NewPolicies(),
	}
	err := c.Pull(testCtx(t))
	if derrors.CodeOf(err) != derrors.CodeFailedPrecondition || !strings.Contains(err.Error(), "docker not found on PATH") {
		t.Fatalf("err = %v, want docker missing as a failed precondition", err)
	}
	if len(d.calls) != 0 {
		t.Fatalf("no docker command may run without docker, got %v", d.calls)
	}
}

func TestPullRetriesThenGivesUp(t *testing.T) {
	d := &scriptedDocker{script: []error{
		dockerFailure("net/http: TLS handshake timeout"),
		dockerFailure("manifest unknown: ghcr.io/behaviorengineering/polypus:9.9.9"),
		nil, // never reached: a missing manifest is not retried.
	}}
	c := Client{
		Project:     "polypus-work",
		ComposeFile: "f.yml",
		Run:         d.run,
		LookPath:    foundDocker,
		Policies:    outbound.NewPolicies(),
	}
	err := c.Pull(testCtx(t))
	if derrors.CodeOf(err) != derrors.CodeNotFound {
		t.Fatalf("err = %v, want CodeNotFound for the missing manifest", err)
	}
	if len(d.calls) != 2 {
		t.Fatalf("docker ran %d times, want 2 (one retry after the TLS timeout, none after the missing manifest)", len(d.calls))
	}
	want := []string{"docker", "compose", "-f", "f.yml", "-p", "polypus-work", "pull"}
	for _, call := range d.calls {
		if !slices.Equal(call, want) {
			t.Fatalf("argv %v want %v", call, want)
		}
	}
}

func TestUpRunsThroughTheRunner(t *testing.T) {
	d := &scriptedDocker{}
	c := Client{ComposeFile: "f.yml", Run: d.run, Policies: outbound.NewPolicies()}
	if err := c.Up(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"docker", "compose", "-f", "f.yml", "up", "-d"}}; !slices.EqualFunc(d.calls, want, slices.Equal) {
		t.Fatalf("calls %v want %v", d.calls, want)
	}
}
