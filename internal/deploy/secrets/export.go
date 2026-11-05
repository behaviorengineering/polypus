package secrets

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
)

// DefaultNames are the secrets every deploy needs, whatever its backends: the
// Tailscale auth key. Backend keys come from the gateway YAML.
var DefaultNames = []string{"TS_AUTHKEY"}

// GatewayEnvFileName is the dotenv the gateway service loads with env_file, written
// beside outPath by Export. It holds every name except DefaultNames, so each key the
// gateway YAML names reaches the gateway container and TS_AUTHKEY does not.
const GatewayEnvFileName = ".env.gateway"

// Export resolves names from the environment, then keyring service app, and writes
// them to outPath as dotenv. Each is required, and each is checked for shape. It also
// writes GatewayEnvFileName beside outPath with the gateway's share of names.
func Export(app string, names []string, outPath string) error {
	if app == "" {
		app = "polypus"
	}
	if len(names) == 0 {
		names = DefaultNames
	}
	secrets := make([]operatorconfig.Secret, len(names))
	for i, n := range names {
		secrets[i] = operatorconfig.Secret{Env: n, Required: true}
	}
	opts := operatorconfig.Options{App: app, Secrets: secrets}
	if err := operatorconfig.ResolveSecrets(opts, nil); err != nil {
		return fmt.Errorf("secrets: %w", err)
	}
	if err := operatorconfig.WriteDotenv(outPath, secrets); err != nil {
		return fmt.Errorf("secrets: %w", err)
	}
	gatewayPath := filepath.Join(filepath.Dir(outPath), GatewayEnvFileName)
	if err := operatorconfig.WriteDotenv(gatewayPath, gatewaySecrets(secrets)); err != nil {
		return fmt.Errorf("secrets: %w", err)
	}
	if slices.Contains(names, cloudflareAccountID) {
		if err := ValidateCloudflareAccountIDFromEnv(app); err != nil {
			return err
		}
	}
	return ValidateAPIKeysFromEnv(app, names)
}

// gatewaySecrets returns secrets without DefaultNames, which only the tailscale
// service reads.
func gatewaySecrets(secrets []operatorconfig.Secret) []operatorconfig.Secret {
	out := make([]operatorconfig.Secret, 0, len(secrets))
	for _, s := range secrets {
		if !slices.Contains(DefaultNames, s.Env) {
			out = append(out, s)
		}
	}
	return out
}
