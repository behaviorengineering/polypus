// Package compose runs docker compose for one deploy environment's project: pull,
// up, recreate and remove services, under the outbound retry policy.
package compose

import (
	"context"
	"os"
	"slices"

	"github.com/behaviorengineering/polypus/internal/deploy/execx"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// Client runs docker compose for one environment's project.
type Client struct {
	// Project is the compose project name (-p), polypus-<identifier>. It prefixes
	// every container and named volume, so environments stay apart on one host.
	Project     string
	ComposeFile string
	EnvFiles    []string
	// Env holds KEY=VALUE pairs the compose file reads, added to the process
	// environment of every docker command (later entries win), so the deploy never
	// changes its own process environment.
	Env []string
}

// Args returns the docker arguments for compose subcommand sub in this project.
func (c Client) Args(sub ...string) []string {
	args := []string{"compose", "-f", c.ComposeFile}
	if c.Project != "" {
		args = append(args, "-p", c.Project)
	}
	for _, ef := range c.EnvFiles {
		args = append(args, "--env-file", ef)
	}
	return append(args, sub...)
}

// Environ is the environment for a docker compose command in this project: the
// process environment, then Env.
func (c Client) Environ() []string {
	return append(os.Environ(), slices.Clone(c.Env)...)
}

func (c Client) runCompose(ctx context.Context, op string, args []string) error {
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, op, "context required")
	}
	err := outbound.Run(ctx, outbound.DepDockerDaemon, func() error {
		err := execx.RunContext(ctx, "docker", args, c.Environ())
		if err == nil {
			return nil
		}
		if dockerExecRetryable(err) {
			return outbound.Retry(err)
		}
		return outbound.Abort(err)
	})
	if err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "docker compose").
			With("project", c.Project)
	}
	return nil
}

// Up runs docker compose up -d.
func (c Client) Up(ctx context.Context) error {
	return c.runCompose(ctx, "compose.Up", c.Args("up", "-d"))
}

// RemoveServices stops and removes the given services' containers; their named
// volumes stay. Services with no container are skipped.
func (c Client) RemoveServices(ctx context.Context, services ...string) error {
	if len(services) == 0 {
		return nil
	}
	return c.runCompose(ctx, "compose.RemoveServices", append(c.Args("rm", "--stop", "--force"), services...))
}

// RecreateServices runs docker compose up -d --force-recreate --no-deps for the given services.
func (c Client) RecreateServices(ctx context.Context, services ...string) error {
	if len(services) == 0 {
		return nil
	}
	args := c.Args("up", "-d", "--force-recreate", "--no-deps")
	return c.runCompose(ctx, "compose.RecreateServices", append(args, services...))
}
