// Package tailnet checks that the machine running a deploy is on the environment's
// tailnet, so health and smoke checks can reach the gateway's tailnet address.
package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/behaviorengineering/polypus/internal/deploy/execx"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// status is the part of `tailscale status --json` the check reads.
type status struct {
	BackendState   string `json:"BackendState"`
	CurrentTailnet *struct {
		MagicDNSSuffix  string `json:"MagicDNSSuffix"`
		MagicDNSEnabled bool   `json:"MagicDNSEnabled"`
	} `json:"CurrentTailnet"`
}

// StatusFunc returns `tailscale status --json` output.
type StatusFunc func(ctx context.Context) ([]byte, error)

// errNoCLI means no tailscale command was found on this machine.
var errNoCLI = errors.New("tailscale CLI not found")

// Check fails unless this machine's Tailscale is running on the tailnet whose
// MagicDNS suffix is want, with MagicDNS on. A nil statusFn runs the tailscale CLI.
// Every failure is CodeFailedPrecondition: the operator must connect this machine.
func Check(ctx context.Context, want string, statusFn StatusFunc) error {
	const op = "tailnet.Check"
	if statusFn == nil {
		statusFn = cliStatus
	}
	out, err := statusFn(ctx)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeFailedPrecondition, op, "this machine must be on tailnet "+want).
			With("tailnet", want)
	}
	var st status
	if err := json.Unmarshal(out, &st); err != nil {
		return derrors.Wrap(err, derrors.CodeFailedPrecondition, op, "parse tailscale status")
	}
	if st.BackendState != "Running" {
		return derrors.New(derrors.CodeFailedPrecondition, op,
			fmt.Sprintf("Tailscale on this machine is %q, not Running; connect it to tailnet %s", st.BackendState, want)).
			With("backend_state", st.BackendState)
	}
	if st.CurrentTailnet == nil {
		return derrors.New(derrors.CodeFailedPrecondition, op, "tailscale status names no current tailnet; connect this machine to "+want)
	}
	got := strings.TrimSuffix(st.CurrentTailnet.MagicDNSSuffix, ".")
	if !strings.EqualFold(got, want) {
		return derrors.New(derrors.CodeFailedPrecondition, op,
			fmt.Sprintf("this machine is on tailnet %s, but the environment's tailnet is %s", got, want))
	}
	if !st.CurrentTailnet.MagicDNSEnabled {
		return derrors.New(derrors.CodeFailedPrecondition, op,
			fmt.Sprintf("MagicDNS is off on %s; turn it on in the Tailscale admin console under DNS", want))
	}
	return nil
}

// cliStatus runs `tailscale status --json` with the first tailscale command found.
func cliStatus(ctx context.Context) ([]byte, error) {
	bin, err := findCLI()
	if err != nil {
		return nil, err
	}
	var out []byte
	err = outbound.Run(ctx, outbound.DepTailscale, func() error {
		raw, runErr := execx.StdoutContext(ctx, bin, []string{"status", "--json"}, nil)
		out = raw
		if runErr == nil {
			return nil
		}
		if tailscaleExecRetryable(runErr) {
			return outbound.Retry(runErr)
		}
		return outbound.Abort(runErr)
	})
	return out, err
}

// tailscaleExecRetryable reports a transient tailscale CLI failure: the process
// was killed, or stderr names a transport blip. A missing CLI, a wrong tailnet,
// and a canceled or expired context are not retried.
func tailscaleExecRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if signaled(err) {
		return true
	}
	stderr := strings.ToLower(execx.Stderr(err))
	for _, p := range []string{"connection refused", "connection reset", "i/o timeout", "temporary failure"} {
		if strings.Contains(stderr, p) {
			return true
		}
	}
	return false
}

func signaled(err error) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	// ExitCode is -1 when the process was terminated by a signal (or never exited).
	return ee.ExitCode() == -1
}

// findCLI looks for tailscale on PATH, then where the desktop apps install it.
func findCLI() (string, error) {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p, nil
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{"/Applications/Tailscale.app/Contents/MacOS/Tailscale"}
	case "windows":
		candidates = []string{`C:\Program Files\Tailscale\tailscale.exe`}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("%w: install Tailscale, or put tailscale on PATH", errNoCLI)
}
