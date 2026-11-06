// Package overlay prunes the gateway's models allow overlay inside the running
// gateway container: slugs the gateway YAML already allows are dropped from it.
package overlay

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/behaviorengineering/polypus/internal/deploy/compose"
	"github.com/behaviorengineering/polypus/internal/deploy/execx"
	"github.com/behaviorengineering/polypus/internal/deploy/gatewayconfig"
	"github.com/behaviorengineering/polypus/internal/deploy/progress"
	"github.com/behaviorengineering/polypus/internal/deploy/stack"
	"github.com/behaviorengineering/polypus/internal/outbound"
	"gopkg.in/yaml.v3"
)

const (
	// DefaultService is the gateway's compose service.
	DefaultService = stack.ServiceGateway
	// DefaultOverlayPath is the models allow overlay inside the gateway container.
	DefaultOverlayPath = "/var/lib/polypus/state/models-allow-overlay.yaml"
)

// ContainerConfig names the environment's compose project, the gateway service in
// it, and the overlay path inside that service's container.
type ContainerConfig struct {
	Compose     compose.Client
	Service     string
	OverlayPath string
	// Out receives progress lines; nil writes to os.Stdout.
	Out io.Writer
}

// PruneContainer reads overlay YAML from the running gateway, prunes git duplicates, and writes back.
func PruneContainer(ctx context.Context, gatewayYAMLPath string, cfg ContainerConfig) error {
	if cfg.Service == "" {
		cfg.Service = DefaultService
	}
	if cfg.OverlayPath == "" {
		cfg.OverlayPath = DefaultOverlayPath
	}
	yamlAllow, err := gatewayconfig.AllowLists(gatewayYAMLPath)
	if err != nil {
		return fmt.Errorf("overlay: %w", err)
	}
	overlay, missing, err := readOverlayFromContainer(ctx, cfg)
	if err != nil {
		return err
	}
	if missing {
		overlay = File{}
	}
	out := progress.New(cfg.Out)
	pruned, removed := Prune(overlay, yamlAllow)
	if equalOverlay(overlay, pruned) {
		out.Println("deploy: overlay prune: no-op")
		return out.Err()
	}
	for _, r := range removed {
		out.Printf("deploy: overlay prune: removed backend=%s model=%s\n", r.Backend, r.Model)
	}
	if err := writeOverlayToContainer(ctx, cfg, pruned); err != nil {
		return err
	}
	return out.Err()
}

func readOverlayFromContainer(ctx context.Context, cfg ContainerConfig) (File, bool, error) {
	args := cfg.Compose.Args("exec", "-T", cfg.Service, "cat", cfg.OverlayPath)
	var out []byte
	var missingFile bool
	err := outbound.Run(ctx, outbound.DepDockerDaemon, func() error {
		raw, runErr := execx.StdoutContext(ctx, "docker", args, cfg.Compose.Environ())
		out = raw
		if runErr == nil {
			return nil
		}
		if overlayMissing(runErr) {
			missingFile = true
			return nil
		}
		if compose.DockerExecRetryable(runErr) {
			return outbound.Retry(runErr)
		}
		return outbound.Abort(runErr)
	})
	if err != nil {
		return File{}, false, fmt.Errorf("overlay: read: %w", err)
	}
	if missingFile {
		return File{}, true, nil
	}
	combined := strings.TrimSpace(string(out))
	if combined == "" {
		return File{}, true, nil
	}
	var file File
	if err := yaml.Unmarshal(out, &file); err != nil {
		return File{}, false, fmt.Errorf("overlay: parse: %w", err)
	}
	return file, false, nil
}

// overlayMissing reports a cat that failed because the overlay file does not exist
// yet, read from the stderr the error carries.
func overlayMissing(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(execx.Stderr(err)), "no such file")
}

func writeOverlayToContainer(ctx context.Context, cfg ContainerConfig, file File) error {
	if file.Backends == nil {
		file.Backends = map[string]Backend{}
	}
	raw, err := yaml.Marshal(file)
	if err != nil {
		return fmt.Errorf("overlay: marshal: %w", err)
	}
	tmp, err := os.CreateTemp("", "models-allow-overlay-*.yaml")
	if err != nil {
		return fmt.Errorf("overlay: temp: %w", err)
	}
	hostPath := tmp.Name()
	defer func() {
		_ = os.Remove(hostPath)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("overlay: chmod temp: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("overlay: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("overlay: close temp: %w", err)
	}
	remoteTmp := cfg.OverlayPath + ".tmp"
	cpArgs := cfg.Compose.Args("cp", hostPath, cfg.Service+":"+remoteTmp)
	mvArgs := cfg.Compose.Args("exec", "-T", cfg.Service, "mv", remoteTmp, cfg.OverlayPath)
	chmodArgs := cfg.Compose.Args("exec", "-T", cfg.Service, "chmod", "600", cfg.OverlayPath)
	env := cfg.Compose.Environ()
	return outbound.Run(ctx, outbound.DepDockerDaemon, func() error {
		if err := execx.RunContext(ctx, "docker", cpArgs, env); err != nil {
			if compose.DockerExecRetryable(err) {
				return outbound.Retry(err)
			}
			return outbound.Abort(fmt.Errorf("overlay: docker cp: %w", err))
		}
		if err := execx.RunContext(ctx, "docker", mvArgs, env); err != nil {
			if compose.DockerExecRetryable(err) {
				return outbound.Retry(err)
			}
			return outbound.Abort(fmt.Errorf("overlay: docker mv: %w", err))
		}
		if err := execx.RunContext(ctx, "docker", chmodArgs, env); err != nil {
			if compose.DockerExecRetryable(err) {
				return outbound.Retry(err)
			}
			return outbound.Abort(fmt.Errorf("overlay: docker chmod: %w", err))
		}
		return nil
	})
}

func equalOverlay(a, b File) bool {
	return maps.EqualFunc(a.Backends, b.Backends, func(x, y Backend) bool {
		return slices.Equal(x.Allow, y.Allow)
	})
}
