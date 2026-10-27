package deploy

import (
	"context"
	"path/filepath"
	"time"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"

	"github.com/behaviorengineering/polypus/internal/deploy/envconfig"
	"github.com/behaviorengineering/polypus/internal/deploy/tailnet"
)

// Options configures one deploy of an environment.
type Options struct {
	// Root is the Polypus checkout that holds the compose file, collector config
	// and repository image pins.
	Root        string
	ComposeFile string
	// RepoPins is the repository's images.env (observability and Tailscale pins).
	RepoPins string
	// PinsFile is an environment's own images file. Empty pins the Polypus and
	// Switchyard images of Version on top of RepoPins.
	PinsFile string
	// ImagesEnv is where the deploy writes the resolved pins when PinsFile is empty.
	ImagesEnv string
	// StateDir holds files the deploy renders per environment, under <StateDir>/<Name>/.
	StateDir  string
	Version   string
	EnvDeploy string
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

// DefaultOptions resolves paths under the Polypus checkout at root.
func DefaultOptions(root string) Options {
	if root == "" {
		root = "."
	}
	root = filepath.Clean(root)
	return Options{
		Root:          root,
		ComposeFile:   filepath.Join(root, "docker-compose.deploy.yml"),
		RepoPins:      filepath.Join(root, "images.env"),
		ImagesEnv:     filepath.Join(root, ".env.images"),
		StateDir:      filepath.Join(root, ".deploy"),
		EnvDeploy:     filepath.Join(root, ".env.deploy"),
		Name:          "polypus",
		HealthTimeout: 120 * time.Second,
		SmokeTimeout:  300 * time.Second,
	}
}
