package deploy

import (
	"context"
	"path/filepath"
	"time"

	"github.com/behaviorengineering/polypus/internal/deploy/envconfig"
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
	Version   string
	EnvDeploy string
	// Environment is the deploy environment the options came from.
	Environment envconfig.Environment
	// GatewayConfig is the environment's gateway YAML, bind-mounted as the Polypus config.
	GatewayConfig string
	// Name is polypus-<identifier>: the compose project, the Tailscale hostname and
	// the keyring service that holds the environment's secrets.
	Name             string
	SecretNames      []string
	SkipPull         bool
	DryRun           bool
	HealthURL        string
	HealthTimeout    time.Duration
	SkipSmoke        bool
	SmokeTimeout     time.Duration
	SmokeChatModel   string
	SmokeRouterModel string
	SmokeCapable     string
	SmokeEfficient   string
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
		EnvDeploy:     filepath.Join(root, ".env.deploy"),
		Name:          "polypus",
		HealthURL:     "http://127.0.0.1:1320",
		HealthTimeout: 120 * time.Second,
		SmokeTimeout:  300 * time.Second,
	}
}
