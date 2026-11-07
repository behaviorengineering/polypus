// Package overlay prunes the gateway's models allow overlay inside the running
// gateway container: slugs the gateway YAML already allows are dropped from it.
package overlay

import (
	"context"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/deploy/compose"
	"github.com/behaviorengineering/polypus/internal/deploy/execx"
	"github.com/behaviorengineering/polypus/internal/deploy/gatewayconfig"
	"github.com/behaviorengineering/polypus/internal/deploy/progress"
	"github.com/behaviorengineering/polypus/internal/deploy/stack"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"gopkg.in/yaml.v3"
)

const (
	// defaultService is the gateway's compose service.
	defaultService = stack.ServiceGateway
	// DefaultOverlayPath is the models allow overlay inside the gateway container:
	// the gateway's state volume, under the overlay's own file name.
	DefaultOverlayPath = "/var/lib/polypus/state/" + config.ModelsAllowOverlayFile
)

// writeScript writes stdin to "$1.tmp" (owner-only), then renames it over "$1",
// all as the container's own user, so the gateway can read what it gets.
const writeScript = `umask 077 && cat > "$1.tmp" && mv "$1.tmp" "$1"`

// ContainerConfig names the environment's compose project, the gateway service in
// it, and the overlay path inside that service's container.
type ContainerConfig struct {
	// Compose runs the docker commands, through its Stdout seam and policies.
	Compose     compose.Client
	Service     string
	OverlayPath string
	// Out receives progress lines; nil writes to os.Stdout.
	Out io.Writer
}

// PruneContainer reads overlay YAML from the running gateway, prunes git duplicates, and writes back.
func PruneContainer(ctx context.Context, gatewayYAMLPath string, cfg ContainerConfig) error {
	const op = "overlay.PruneContainer"
	if cfg.Service == "" {
		cfg.Service = defaultService
	}
	if cfg.OverlayPath == "" {
		cfg.OverlayPath = DefaultOverlayPath
	}
	yamlAllow, err := gatewayconfig.AllowLists(gatewayYAMLPath)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInvalid, op, "gateway allow lists")
	}
	current, err := readOverlayFromContainer(ctx, cfg)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "read "+cfg.OverlayPath).
			With("service", cfg.Service)
	}
	out := progress.New(cfg.Out)
	pruned, removed := prune(current, yamlAllow)
	if equalOverlay(current, pruned) {
		out.Println("deploy: overlay prune: no-op")
		return out.Err()
	}
	for _, r := range removed {
		out.Printf("deploy: overlay prune: removed backend=%s model=%s\n", r.Backend, r.Model)
	}
	if err := writeOverlayToContainer(ctx, cfg, pruned); err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "write "+cfg.OverlayPath).
			With("service", cfg.Service)
	}
	return out.Err()
}

// readOverlayFromContainer returns the overlay in the gateway container; one that
// does not exist yet, or is empty, is an empty overlay.
func readOverlayFromContainer(ctx context.Context, cfg ContainerConfig) (file, error) {
	raw, err := cfg.Compose.Exec(ctx, "overlay.read", nil, cfg.Service, "cat", cfg.OverlayPath)
	if err != nil {
		if overlayMissing(err) {
			return file{}, nil
		}
		return file{}, err
	}
	if strings.TrimSpace(string(raw)) == "" {
		return file{}, nil
	}
	var f file
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return file{}, derrors.Wrap(err, derrors.CodeInvalid, "overlay.read", "parse")
	}
	return f, nil
}

// overlayMissing reports a cat that failed because the overlay file does not exist
// yet, read from the stderr the error carries.
func overlayMissing(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(execx.Stderr(err)), "no such file")
}

// writeOverlayToContainer replaces the overlay in one docker exec: the YAML goes
// in on stdin and is renamed into place, so a retry rewrites the same file.
func writeOverlayToContainer(ctx context.Context, cfg ContainerConfig, f file) error {
	if f.Backends == nil {
		f.Backends = map[string]backend{}
	}
	raw, err := yaml.Marshal(f)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "overlay.write", "marshal")
	}
	_, err = cfg.Compose.Exec(ctx, "overlay.write", raw, cfg.Service, "sh", "-c", writeScript, "sh", cfg.OverlayPath)
	return err
}

func equalOverlay(a, b file) bool {
	return maps.EqualFunc(a.Backends, b.Backends, func(x, y backend) bool {
		return slices.Equal(x.Allow, y.Allow)
	})
}
