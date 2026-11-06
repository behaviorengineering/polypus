package compose

import (
	"context"
	"os/exec"
	"strings"

	"github.com/behaviorengineering/polypus/internal/deploy/execx"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// dockerExitDaemon is the docker CLI's own exit status when docker itself failed
// (for example the daemon is unreachable), as opposed to the command it ran.
const dockerExitDaemon = 125

// transientStderr are docker stderr phrases of a registry or network blip worth
// another attempt. They are matched against docker's stderr, never its argv.
var transientStderr = []string{
	"cannot connect to the docker daemon",
	"is the docker daemon running",
	"toomanyrequests",
	"429 too many requests",
	"tls handshake timeout",
	"i/o timeout",
	"connection reset by peer",
	"connection refused",
	"temporary failure in name resolution",
	"net/http: request canceled while waiting for connection",
	"unexpected eof",
}

// manifestMissingStderr are docker pull stderr phrases of an image that is not
// there (or not readable), which no retry fixes.
var manifestMissingStderr = []string{
	"manifest unknown",
	"repository does not exist",
	"pull access denied for",
	"denied requested access to the resource",
}

// Pull runs docker compose pull with retries for transient docker/network failures only.
func (c Client) Pull(ctx context.Context) error {
	const op = "compose.Pull"
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, op, "context required")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return derrors.Wrap(err, derrors.CodeFailedPrecondition, op, "docker not found on PATH (is Docker installed?)")
	}
	args := c.Args("pull")
	err := outbound.Run(ctx, outbound.DepRegistry, func() error {
		err := execx.RunContext(ctx, "docker", args, c.Environ())
		if err == nil {
			return nil
		}
		if pullManifestMissing(err) {
			return outbound.Abort(err)
		}
		if dockerExecRetryable(err) {
			return outbound.Retry(err)
		}
		return outbound.Abort(err)
	})
	if err != nil {
		code := derrors.CodeUnavailable
		if pullManifestMissing(err) {
			code = derrors.CodeNotFound
		}
		return derrors.Wrap(err, code, op, "docker compose pull").With("project", c.Project)
	}
	return nil
}

// pullManifestMissing reports a pull that failed because an image is missing or
// refused, read from docker's stderr.
func pullManifestMissing(err error) bool {
	return stderrContains(err, manifestMissingStderr)
}

// DockerExecRetryable reports a transient docker CLI failure: docker itself failed
// (exit 125), or its stderr names a registry or network blip. It never reads argv.
func DockerExecRetryable(err error) bool {
	return dockerExecRetryable(err)
}

func dockerExecRetryable(err error) bool {
	if err == nil {
		return false
	}
	if execx.ExitCode(err) == dockerExitDaemon {
		return true
	}
	return stderrContains(err, transientStderr)
}

func stderrContains(err error, phrases []string) bool {
	stderr := strings.ToLower(execx.Stderr(err))
	if stderr == "" {
		return false
	}
	for _, p := range phrases {
		if strings.Contains(stderr, p) {
			return true
		}
	}
	return false
}
