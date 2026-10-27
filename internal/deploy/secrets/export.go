package secrets

import (
	"fmt"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
)

// Default secret env names for homelab deploy.
var DefaultNames = []string{"CF_AI_API_KEY", "CF_ACCOUNT_ID", "TS_AUTHKEY"}

// Export resolves secrets via operatorconfig and writes dotenv.
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
	if err := ValidateCloudflareFromEnv(); err != nil {
		return err
	}
	return ValidateOptionalAPIKeysFromEnv(names)
}
