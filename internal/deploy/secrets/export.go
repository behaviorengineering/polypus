package secrets

import (
	"fmt"
	"slices"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
)

// DefaultNames are the secrets every deploy needs, whatever its backends: the
// Tailscale auth key. Backend keys come from the gateway YAML.
var DefaultNames = []string{"TS_AUTHKEY"}

// Export resolves names from the environment, then keyring service app, and writes
// them to outPath as dotenv. Each is required, and each is checked for shape.
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
	if slices.Contains(names, cloudflareAccountID) {
		if err := ValidateCloudflareAccountIDFromEnv(app); err != nil {
			return err
		}
	}
	return ValidateAPIKeysFromEnv(app, names)
}
