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
	return execx.StdoutContext(ctx, bin, []string{"status", "--json"}, nil)
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
