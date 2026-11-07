package cli

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/spf13/cobra"
)

// tracingEnv points tracing at a closed local port and a relative dump root under
// a temp working directory, and returns that root as an absolute path.
func tracingEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("POLYPUS_OTEL", "1")
	t.Setenv("POLYPUS_OTLP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("POLYPUS_FAILURE_DUMP_DIR", filepath.Join("logs", "dumps"))
	abs, err := filepath.Abs(filepath.Join("logs", "dumps"))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// TestWithTracingLogsWhereDumpsGo covers the one helper serve and the job commands
// share: it names the command and the absolute dump root, hands run the config,
// and flushes from a context that keeps the caller's values.
func TestWithTracingLogsWhereDumpsGo(t *testing.T) {
	dumpRoot := tracingEnv(t)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "v")

	var sawCtx bool
	err := withTracingOpts(ctx, "polypus test", tracingOptions{flush: time.Second, logger: logger},
		func(runCtx context.Context, cfg observability.Config) error {
			sawCtx = runCtx.Value(key{}) == "v"
			if cfg.ActiveDumpDir() == "" {
				t.Error("run got a config with no dump dir")
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if !sawCtx {
		t.Fatal("run did not get the caller's context")
	}
	got := buf.String()
	if !strings.Contains(got, `command="polypus test"`) || !strings.Contains(got, "dump="+dumpRoot) {
		t.Fatalf("otel line must name the command and the absolute dump root %s, got %q", dumpRoot, got)
	}
}

func TestWithTracingReturnsRunError(t *testing.T) {
	tracingEnv(t)
	want := errors.New("job failed")
	err := withTracingOpts(context.Background(), "polypus test", tracingOptions{flush: time.Second, logger: slog.New(slog.DiscardHandler)},
		func(context.Context, observability.Config) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("want run's error, got %v", err)
	}
}

func TestWithTracingBadConfigExits2(t *testing.T) {
	tracingEnv(t)
	t.Setenv("POLYPUS_OTEL", "maybe")
	ran := false
	cmd := &cobra.Command{Use: "job"}
	cmd.SetContext(context.Background())
	err := withTracing(cmd, func(context.Context) error {
		ran = true
		return nil
	})
	var ex *exitError
	if !errors.As(err, &ex) || ex.code != 2 || !strings.Contains(ex.msg, "job: tracing config") {
		t.Fatalf("want exit 2 naming the command, got %v", err)
	}
	if ran {
		t.Fatal("run must not start when tracing config is bad")
	}
}
