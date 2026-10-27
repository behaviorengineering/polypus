package compose

import (
	"context"
	"fmt"
	"os"

	"github.com/behaviorengineering/polypus/internal/deploy/execx"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// Client runs docker compose for homelab deploy.
type Client struct {
	ComposeFile string
	EnvFiles    []string
}

func (c Client) baseArgs() []string {
	args := []string{"compose", "-f", c.ComposeFile}
	for _, ef := range c.EnvFiles {
		args = append(args, "--env-file", ef)
	}
	return args
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
	args := append(c.baseArgs(), "up", "-d")
	return c.runCompose(ctx, args)
}

// RecreateServices runs docker compose up -d --force-recreate --no-deps for the given services.
func (c Client) RecreateServices(ctx context.Context, services ...string) error {
	if len(services) == 0 {
		return nil
	}
	args := append(c.baseArgs(), "up", "-d", "--force-recreate", "--no-deps")
	args = append(args, services...)
	return c.runCompose(ctx, args)
}
