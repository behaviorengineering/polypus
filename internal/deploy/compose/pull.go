package compose

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/behaviorengineering/polypus/internal/deploy/execx"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// Pull runs docker compose pull with retries for transient docker/network failures only.
func (c Client) Pull(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("compose: context required")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("compose: docker not found on PATH (is Docker Desktop running?)")
	}
	args := append(c.baseArgs(), "pull")
	return outbound.Run(ctx, outbound.DepRegistry, func() error {
		err := execx.RunContext(ctx, "docker", args, c.env())
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
}

func pullManifestMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "manifest unknown") ||
		strings.Contains(msg, "repository does not exist") ||
		strings.Contains(msg, "pull access denied for") ||
		strings.Contains(msg, "denied requested access to the resource")
}

// DockerExecRetryable reports transient docker CLI failures.
func DockerExecRetryable(err error) bool {
	return dockerExecRetryable(err)
}

func dockerExecRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "connection") ||
		strings.Contains(msg, "429") ||
		strings.Contains(msg, "temporarily") ||
		strings.Contains(msg, "daemon")
}
