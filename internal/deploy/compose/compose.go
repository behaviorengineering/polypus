// Package compose runs docker compose for one deploy environment's project: pull,
// up, recreate and remove services, under the outbound retry policy.
package compose

import (
	"context"
	"os"
	"os/exec"
	"slices"

	"github.com/behaviorengineering/polypus/internal/deploy/execx"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// Runner runs one docker command with env and returns its failure; the default is
// execx.RunContext.
type Runner func(ctx context.Context, name string, args, env []string) error

// OutputRunner runs one docker command with env and returns its combined output,
// for text a person reads; the default is execx.OutputContext.
type OutputRunner func(ctx context.Context, name string, args, env []string) ([]byte, error)

// StdoutRunner runs one docker command with env, feeds it stdin (nil for none) and
// returns its stdout alone, for output a program reads; the default is
// execx.StdoutInputContext.
type StdoutRunner func(ctx context.Context, name string, args, env []string, stdin []byte) ([]byte, error)

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
	// Run runs each docker command; nil uses execx.RunContext.
	Run Runner
	// Output runs each docker command whose output a person reads; nil uses
	// execx.OutputContext.
	Output OutputRunner
	// Stdout runs each docker command whose stdout a program reads, or that takes
	// stdin (Exec); nil uses execx.StdoutInputContext.
	Stdout StdoutRunner
	// LookPath finds the docker binary before a pull; nil uses exec.LookPath.
	LookPath func(file string) (string, error)
	// Policies holds the retry and breaker state for docker and registry calls;
	// nil uses the process default.
	Policies *outbound.Policies
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

// run runs docker with args in this project's environment, through Run.
func (c Client) run(ctx context.Context, args []string) error {
	run := c.Run
	if run == nil {
		run = execx.RunContext
	}
	return run(ctx, "docker", args, c.Environ())
}

// CombinedOutput runs docker with args in this project's environment and returns
// what it wrote, through Output. It is for output a person reads, such as logs.
func (c Client) CombinedOutput(ctx context.Context, args []string) ([]byte, error) {
	output := c.Output
	if output == nil {
		output = execx.OutputContext
	}
	return output(ctx, "docker", args, c.Environ())
}

// Exec runs cmd in service's running container (docker compose exec -T), feeding
// it stdin when non-nil, and returns its stdout. A transient docker failure is
// retried under the docker daemon policy, so cmd must be safe to run twice. A
// failure names op and the service, and keeps the command's stderr in its chain.
func (c Client) Exec(ctx context.Context, op string, stdin []byte, service string, cmd ...string) ([]byte, error) {
	if ctx == nil {
		return nil, derrors.New(derrors.CodeInvalid, op, "context required")
	}
	stdout := c.Stdout
	if stdout == nil {
		stdout = execx.StdoutInputContext
	}
	args := c.Args(append([]string{"exec", "-T", service}, cmd...)...)
	var out []byte
	err := c.Policies.Run(ctx, outbound.DepDockerDaemon, func() error {
		raw, err := stdout(ctx, "docker", args, c.Environ(), stdin)
		out = raw
		if err == nil {
			return nil
		}
		if DockerExecRetryable(err) {
			return outbound.Retry(err)
		}
		return outbound.Abort(err)
	})
	if err != nil {
		return out, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "docker compose exec "+service).
			With("project", c.Project)
	}
	return out, nil
}

// lookDocker finds the docker binary through LookPath.
func (c Client) lookDocker() error {
	look := c.LookPath
	if look == nil {
		look = exec.LookPath
	}
	_, err := look("docker")
	return err
}

func (c Client) runCompose(ctx context.Context, op string, args []string) error {
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, op, "context required")
	}
	err := c.Policies.Run(ctx, outbound.DepDockerDaemon, func() error {
		err := c.run(ctx, args)
		if err == nil {
			return nil
		}
		if DockerExecRetryable(err) {
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
