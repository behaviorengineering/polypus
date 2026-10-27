package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// RunContext streams stdout/stderr and honors ctx cancellation.
func RunContext(ctx context.Context, name string, args []string, env []string) error {
	if ctx == nil {
		return fmt.Errorf("execx: context required")
	}
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: name and args are the deploy's own docker commands.
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("%s %v: exit %d", name, args, ee.ExitCode())
		}
		return fmt.Errorf("%s %v: %w", name, args, err)
	}
	return nil
}

// OutputContext runs a command with context, optional stdin, and returns combined output.
func OutputContext(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("execx: context required")
	}
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: name and args are the deploy's own docker commands.
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %v: %w", name, args, err)
	}
	return out, nil
}
