package deploy

import (
	"github.com/behaviorengineering/polypus/internal/deploy/gatewayconfig"
	"github.com/behaviorengineering/polypus/internal/deploy/paths"
	"github.com/behaviorengineering/polypus/internal/deploy/secrets"
)

// ExportSecretNames returns keyring env names for export-env and deploy.
// Explicit opts.SecretNames wins; otherwise names come from the gateway YAML
// (bearer_env plus gemini extension defaults) after the default Cloudflare and
// Tailscale names.
func ExportSecretNames(opts Options) ([]string, error) {
	if len(opts.SecretNames) > 0 {
		return opts.SecretNames, nil
	}
	gatewayPath, err := paths.ResolveGatewayConfig(opts.GatewayConfig)
	if err != nil {
		return nil, err
	}
	return gatewayconfig.SecretEnvNames(gatewayPath, secrets.DefaultNames)
}
