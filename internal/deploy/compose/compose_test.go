package compose

import (
	"context"
	"errors"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/deploy/execx"
)

func TestArgsProject(t *testing.T) {
	c := Client{Project: "polypus-work", ComposeFile: "/repo/docker-compose.deploy.yml", EnvFiles: []string{"/repo/.env.images", "/repo/.env.deploy"}}
	got := c.Args("exec", "-T", "gateway", "cat", "/x")
	want := []string{
		"compose", "-f", "/repo/docker-compose.deploy.yml", "-p", "polypus-work",
		"--env-file", "/repo/.env.images", "--env-file", "/repo/.env.deploy",
		"exec", "-T", "gateway", "cat", "/x",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("args %v\nwant %v", got, want)
	}
}

func TestArgsWithoutProject(t *testing.T) {
	got := Client{ComposeFile: "f.yml"}.Args("up", "-d")
	if want := []string{"compose", "-f", "f.yml", "up", "-d"}; !slices.Equal(got, want) {
		t.Fatalf("args %v want %v", got, want)
	}
}

func TestEnvironAddsEnvLast(t *testing.T) {
	t.Setenv("COMPOSE_PROFILES", "routers")
	env := Client{Env: []string{"COMPOSE_PROFILES="}}.Environ()
	if env[len(env)-1] != "COMPOSE_PROFILES=" {
		t.Fatalf("Env must come last so it wins over the process environment: %v", env[len(env)-1])
	}
	if os.Getenv("COMPOSE_PROFILES") != "routers" {
		t.Fatal("Environ must not change the process environment")
	}
}

func runErr(t *testing.T, script string) error {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// The argv names "connection" and "daemon" on purpose: they must not count.
	return execx.RunContext(ctx, "sh", []string{"-c", script, "connection-daemon-timeout"}, nil)
}

func TestDockerExecRetryableByExitCodeAndStderr(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
		want   bool
	}{
		{"docker itself failed", "exit 125", true},
		{"registry throttled", "echo 'Error: toomanyrequests: rate limit' >&2; exit 1", true},
		{"tls timeout", "echo 'net/http: TLS handshake timeout' >&2; exit 1", true},
		{"command failed, argv mentions daemon", "echo 'service gateway failed' >&2; exit 1", false},
		{"silent failure", "exit 1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DockerExecRetryable(runErr(t, tc.script)); got != tc.want {
				t.Fatalf("retryable %v want %v", got, tc.want)
			}
		})
	}
	if DockerExecRetryable(errors.New("connection timeout from the daemon")) {
		t.Fatal("text with no docker run behind it is not retried")
	}
}

func TestPullManifestMissingReadsStderr(t *testing.T) {
	if !pullManifestMissing(runErr(t, "echo 'manifest unknown: ghcr.io/x:1' >&2; exit 1")) {
		t.Fatal("want manifest unknown in stderr detected")
	}
	if pullManifestMissing(runErr(t, "exit 1")) {
		t.Fatal("no stderr, no match")
	}
}
