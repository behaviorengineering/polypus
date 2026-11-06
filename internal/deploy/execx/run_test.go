package execx

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func shell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestStdoutContextKeepsStderrOutOfOutput(t *testing.T) {
	shell(t)
	out, err := StdoutContext(testContext(t), "sh", []string{"-c", `echo 'WARN variable is not set' >&2; echo '{"ok":true}'`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != `{"ok":true}` {
		t.Fatalf("stdout %q: stderr leaked into the parsed output", got)
	}
}

func TestStdoutContextErrorCarriesStderrAndExit(t *testing.T) {
	shell(t)
	_, err := StdoutContext(testContext(t), "sh", []string{"-c", "echo 'cat: no such file' >&2; exit 3"}, nil)
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ExitCode(err) != 3 {
		t.Fatalf("err = %v, want the exit status kept in the chain", err)
	}
	if !strings.Contains(Stderr(err), "no such file") || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("err = %v, want the stderr tail", err)
	}
}

func TestRunContextKeepsExitError(t *testing.T) {
	shell(t)
	err := RunContext(testContext(t), "sh", []string{"-c", "echo 'daemon down' >&2; exit 125"}, nil)
	if ExitCode(err) != 125 {
		t.Fatalf("exit %d from %v, want 125", ExitCode(err), err)
	}
	if !strings.Contains(Stderr(err), "daemon down") {
		t.Fatalf("stderr %q, want the tail", Stderr(err))
	}
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	tb := &tailBuffer{limit: 4}
	_, _ = tb.Write([]byte("abcdef"))
	_, _ = tb.Write([]byte("gh"))
	if got := tb.String(); got != "efgh" {
		t.Fatalf("tail %q, want efgh", got)
	}
}

func TestExitCodeWithoutExit(t *testing.T) {
	if ExitCode(errors.New("x")) != -1 {
		t.Fatal("want -1 for an error with no exited command")
	}
}
