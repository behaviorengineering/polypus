package overlay

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/behaviorengineering/polypus/internal/deploy/compose"
	"github.com/behaviorengineering/polypus/internal/deploy/execx"
	"github.com/behaviorengineering/polypus/internal/deploy/gatewayconfig"
	"github.com/behaviorengineering/polypus/internal/outbound"
	"gopkg.in/yaml.v3"
)

const (
	DefaultGatewayContainer = "polypus-gateway"
	DefaultOverlayPath      = "/var/lib/polypus/state/models-allow-overlay.yaml"
	dockerSubcommandExec    = "exec"
)

// ContainerConfig names the gateway container and overlay path inside it.
type ContainerConfig struct {
	Container   string
	OverlayPath string
}

// DefaultContainerConfig returns homelab deploy defaults.
func DefaultContainerConfig() ContainerConfig {
	return ContainerConfig{
		Container:   DefaultGatewayContainer,
		OverlayPath: DefaultOverlayPath,
	}
}

// PruneContainer reads overlay YAML from the running gateway, prunes git duplicates, and writes back.
func PruneContainer(ctx context.Context, gatewayYAMLPath string, cfg ContainerConfig) error {
	if cfg.Container == "" {
		cfg = DefaultContainerConfig()
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
	pruned, removed := Prune(overlay, yamlAllow)
	if equalOverlay(overlay, pruned) {
		fmt.Println("deploy: overlay prune: no-op")
		return nil
	}
	for _, r := range removed {
		fmt.Printf("deploy: overlay prune: removed backend=%s model=%s\n", r.Backend, r.Model)
	}
	return writeOverlayToContainer(ctx, cfg, pruned)
}

func readOverlayFromContainer(ctx context.Context, cfg ContainerConfig) (File, bool, error) {
	args := []string{dockerSubcommandExec, cfg.Container, "cat", cfg.OverlayPath}
	var out []byte
	var missingFile bool
	err := outbound.Run(ctx, outbound.DepDockerDaemon, func() error {
		raw, runErr := execx.OutputContext(ctx, "docker", args, nil)
		out = raw
		if runErr == nil {
			return nil
		}
		if overlayMissing(runErr, raw) {
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

func overlayMissing(err error, out []byte) bool {
	if err == nil && len(out) == 0 {
		return true
	}
	msg := strings.ToLower(string(out))
	if err != nil {
		msg += " " + strings.ToLower(err.Error())
	}
	return strings.Contains(msg, "no such file")
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
	cpArgs := []string{"cp", hostPath, cfg.Container + ":" + remoteTmp}
	mvArgs := []string{dockerSubcommandExec, cfg.Container, "mv", remoteTmp, cfg.OverlayPath}
	chmodArgs := []string{dockerSubcommandExec, cfg.Container, "chmod", "600", cfg.OverlayPath}
	return outbound.Run(ctx, outbound.DepDockerDaemon, func() error {
		if err := execx.RunContext(ctx, "docker", cpArgs, nil); err != nil {
			if compose.DockerExecRetryable(err) {
				return outbound.Retry(err)
			}
			return outbound.Abort(fmt.Errorf("overlay: docker cp: %w", err))
		}
		if err := execx.RunContext(ctx, "docker", mvArgs, nil); err != nil {
			if compose.DockerExecRetryable(err) {
				return outbound.Retry(err)
			}
			return outbound.Abort(fmt.Errorf("overlay: docker mv: %w", err))
		}
		if err := execx.RunContext(ctx, "docker", chmodArgs, nil); err != nil {
			if compose.DockerExecRetryable(err) {
				return outbound.Retry(err)
			}
			return outbound.Abort(fmt.Errorf("overlay: docker chmod: %w", err))
		}
		return nil
	})
}

func equalOverlay(a, b File) bool {
	if len(a.Backends) != len(b.Backends) {
		return false
	}
	for k, av := range a.Backends {
		bv, ok := b.Backends[k]
		if !ok || !equalAllow(av.Allow, bv.Allow) {
			return false
		}
	}
	return true
}

func equalAllow(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// PruneContainerDefault is a convenience wrapper for deploy.
func PruneContainerDefault(ctx context.Context, gatewayYAMLPath string) error {
	return PruneContainer(ctx, gatewayYAMLPath, DefaultContainerConfig())
}
