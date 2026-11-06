// Package secrets resolves a deploy environment's secrets from the process
// environment and its keyring service, writes them as dotenv files for compose,
// and checks their shape.
package secrets

import (
	"path/filepath"
	"slices"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// DefaultNames are the secrets every deploy needs, whatever its backends: the
// Tailscale auth key. Backend keys come from the gateway YAML.
var DefaultNames = []string{"TS_AUTHKEY"}

// GatewayEnvFileName is the dotenv the gateway service loads with env_file, written
// beside outPath by Export. It holds every name except DefaultNames, so each key the
// gateway YAML names reaches the gateway container and TS_AUTHKEY does not.
const GatewayEnvFileName = ".env.gateway"

// Export resolves names from the environment, then the OS keyring service app, and
// writes them as ExportWithKeyring does.
func Export(app string, names []string, outPath string) error {
	return ExportWithKeyring(nil, app, names, outPath)
}

// ExportWithKeyring resolves names from the environment, then keyring service app
// in kr (nil uses the OS keyring), and writes them to outPath as dotenv. Each is
// required, and each is checked for shape. It also writes GatewayEnvFileName beside
// outPath with the gateway's share of names.
func ExportWithKeyring(kr operatorconfig.Keyring, app string, names []string, outPath string) error {
	const op = "secrets.Export"
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
	if err := operatorconfig.ResolveSecrets(opts, kr); err != nil {
		return derrors.Wrap(err, derrors.CodeFailedPrecondition, op, "resolve secrets (store each with: polypus deploy secret set <ENV>)").
			With("keyring_service", app)
	}
	if err := operatorconfig.WriteDotenv(outPath, secrets); err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, op, "write "+outPath)
	}
	gatewayPath := filepath.Join(filepath.Dir(outPath), GatewayEnvFileName)
	if err := operatorconfig.WriteDotenv(gatewayPath, gatewaySecrets(secrets)); err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, op, "write "+gatewayPath)
	}
	if slices.Contains(names, cloudflareAccountID) {
		if err := validateCloudflareAccountIDFromEnv(app); err != nil {
			return derrors.Wrap(err, derrors.CodeFailedPrecondition, op, "check "+cloudflareAccountID).With("keyring_service", app)
		}
	}
	if err := validateAPIKeysFromEnv(app, names); err != nil {
		return derrors.Wrap(err, derrors.CodeFailedPrecondition, op, "check API keys").With("keyring_service", app)
	}
	return nil
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
