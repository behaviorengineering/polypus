package compose

import (
	"context"
	"fmt"
	"os"

	"github.com/behaviorengineering/polypus/internal/deploy/execx"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// Client runs docker compose for one environment's project.
type Client struct {
	// Project is the compose project name (-p), polypus-<identifier>. It prefixes
	// every container and named volume, so environments stay apart on one host.
	Project     string
	ComposeFile string
	EnvFiles    []string
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

func (c Client) env() []string {
	return os.Environ()
}

func (c Client) runCompose(ctx context.Context, args []string) error {
	if ctx == nil {
		return fmt.Errorf("compose: context required")
	}
	return outbound.Run(ctx, outbound.DepDockerDaemon, func() error {
		err := execx.RunContext(ctx, "docker", args, c.env())
		if err == nil {
			return nil
		}
		if dockerExecRetryable(err) {
			return outbound.Retry(err)
		}
		return outbound.Abort(err)
	})
}

// Up runs docker compose up -d.
func (c Client) Up(ctx context.Context) error {
	return c.runCompose(ctx, c.Args("up", "-d"))
}

// RecreateServices runs docker compose up -d --force-recreate --no-deps for the given services.
func (c Client) RecreateServices(ctx context.Context, services ...string) error {
	if len(services) == 0 {
		return nil
	}
	args := c.Args("up", "-d", "--force-recreate", "--no-deps")
	return c.runCompose(ctx, append(args, services...))
}
