package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"

	"github.com/behaviorengineering/polypus/internal/deploy/envconfig"
	"github.com/behaviorengineering/polypus/internal/deploy/tailnet"
)

// Options configures one deploy of an environment.
type Options struct {
	// PinsFile is an environment's own images file. Empty pins the Polypus and
	// Switchyard images of Version on top of the embedded stack's images.env.
	PinsFile string
	// StateDir holds what each deploy writes, one folder per environment: see Dir.
	StateDir string
	Version  string
	// Environment is the deploy environment the options came from.
	Environment envconfig.Environment
	// GatewayConfig is the environment's gateway YAML, bind-mounted as the Polypus config.
	GatewayConfig string
	// Name is polypus-<identifier>: the compose project, the Tailscale hostname and
	// the keyring service that holds the environment's secrets.
	Name        string
	SecretNames []string
	SkipPull    bool
	DryRun      bool
	// HealthURL is the gateway's base URL for health and smoke checks: the
	// environment's tailnet address unless overridden.
	HealthURL string
	// Tailnet, when set, is checked before the deploy changes anything: this
	// machine must be on it to reach HealthURL.
	Tailnet string
	// Keyring holds the environment's secrets; nil uses the OS keyring.
	Keyring operatorconfig.Keyring
	// TailnetStatus reads `tailscale status --json`; nil runs the tailscale CLI.
	TailnetStatus    tailnet.StatusFunc
	HealthTimeout    time.Duration
	SkipSmoke        bool
	SmokeTimeout     time.Duration
	SmokeChatModel   string
	SkipOverlayPrune bool
	PruneFunc        func(ctx context.Context, gatewayPath string) error
}

// StateDirEnv overrides where deploys write their per-environment folders.
const StateDirEnv = "POLYPUS_DEPLOY_STATE_DIR"

// DefaultStateDir is POLYPUS_DEPLOY_STATE_DIR, else polypus/deploy under the user
// cache folder (~/Library/Caches on macOS, ~/.cache on Linux, %LocalAppData% on
// Windows). Nothing there needs keeping: each deploy rewrites it.
func DefaultStateDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(StateDirEnv)); dir != "" {
		return filepath.Abs(dir)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("deploy: user cache folder: %w (set %s)", err, StateDirEnv)
	}
	return filepath.Join(cache, "polypus", "deploy"), nil
}

// DefaultOptions returns deploy defaults writing under stateDir.
func DefaultOptions(stateDir string) Options {
	return Options{
		StateDir:      filepath.Clean(stateDir),
		Name:          "polypus",
		HealthTimeout: 120 * time.Second,
		SmokeTimeout:  300 * time.Second,
	}
}

// Dir is the environment's folder: the stack, serve.json, image pins and resolved
// secrets the deploy writes, so two environments share nothing.
func (o Options) Dir() string {
	return filepath.Join(o.StateDir, o.Name)
}

// ImagesEnvPath is where the deploy writes the resolved image pins.
func (o Options) ImagesEnvPath() string {
	return filepath.Join(o.Dir(), ".env.images")
}

// EnvDeployPath is where the deploy writes the resolved secrets (mode 0600).
func (o Options) EnvDeployPath() string {
	return filepath.Join(o.Dir(), ".env.deploy")
}
