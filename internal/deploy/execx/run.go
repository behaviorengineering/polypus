// Package execx runs the deploy's docker and tailscale commands under the caller's
// context, streaming or capturing their output. A failed command returns a
// *RunError that keeps the *exec.ExitError (for exit-code checks) and the tail of
// what the command wrote to stderr (for message checks).
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// stderrTail bounds how much of a command's stderr a RunError keeps.
const stderrTail = 4 << 10

// RunError is a command that failed to start or exited non-zero.
type RunError struct {
	Name string
	Args []string
	// Stderr is the end of what the command wrote to stderr, at most 4 KiB.
	Stderr string
	Err    error
}

func (e *RunError) Error() string {
	msg := fmt.Sprintf("%s %v: %v", e.Name, e.Args, e.Err)
	if s := strings.TrimSpace(e.Stderr); s != "" {
		msg += ": " + s
	}
	return msg
}

// Unwrap returns the cause, an *exec.ExitError when the command ran and failed.
func (e *RunError) Unwrap() error { return e.Err }

// Stderr returns the stderr tail of the first RunError in err's chain, or "".
func Stderr(err error) string {
	var re *RunError
	if errors.As(err, &re) {
		return re.Stderr
	}
	return ""
}

// ExitCode returns the exit status of the command that failed in err's chain, or
// -1 when err holds no exited command.
func ExitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.limit; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func command(ctx context.Context, name string, args, env []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: name and args are the deploy's own docker and tailscale commands.
	cmd.Env = env
	return cmd
}

// RunContext runs a command with env (nil inherits the process environment),
// streaming its stdout and stderr to the process's own. On failure the error also
// carries the stderr tail.
func RunContext(ctx context.Context, name string, args []string, env []string) error {
	if ctx == nil {
		return fmt.Errorf("execx: context required")
	}
	cmd := command(ctx, name, args, env)
	tail := &tailBuffer{limit: stderrTail}
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, tail)
	if err := cmd.Run(); err != nil {
		return &RunError{Name: name, Args: args, Stderr: tail.String(), Err: err}
	}
	return nil
}

// OutputContext runs a command and returns what it wrote to stdout and stderr
// together, for output a person reads (such as container logs). Never parse it.
func OutputContext(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("execx: context required")
	}
	out, err := command(ctx, name, args, env).CombinedOutput()
	if err != nil {
		return out, &RunError{Name: name, Args: args, Err: err}
	}
	return out, nil
}

// StdoutContext runs a command with env (nil inherits the process environment) and
// returns its stdout alone, so machine output (JSON, YAML) is not mixed with
// warnings. On failure the error carries the stderr tail.
func StdoutContext(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
	return StdoutInputContext(ctx, name, args, env, nil)
}

// StdoutInputContext is StdoutContext with stdin fed to the command (nil for none).
func StdoutInputContext(ctx context.Context, name string, args, env []string, stdin []byte) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("execx: context required")
	}
	cmd := command(ctx, name, args, env)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	tail := &tailBuffer{limit: stderrTail}
	cmd.Stderr = tail
	out, err := cmd.Output()
	if err != nil {
		return out, &RunError{Name: name, Args: args, Stderr: tail.String(), Err: err}
	}
	return out, nil
}
