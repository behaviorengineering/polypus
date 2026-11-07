// Package gatewayconfig reads what a deploy needs from an environment's gateway
// YAML: the secrets its backends name, its routers, and the smoke probes to run.
package gatewayconfig

import (
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"gopkg.in/yaml.v3"
)

// placeholderPattern matches a ${VAR} placeholder the gateway expands from env.
var placeholderPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

type gatewayYAML struct {
	SystemOneBackend capabilityBackend      `yaml:"systemone_backend"`
	Backends         map[string]backendYAML `yaml:"backends"`
}

type capabilityBackend struct {
	Enabled *bool `yaml:"enabled"`
}

type backendYAML struct {
	Extension    string          `yaml:"extension"`
	BaseURL      string          `yaml:"base_url"`
	Auth         backendAuthYAML `yaml:"auth"`
	Capabilities []string        `yaml:"capabilities"`
	Models       modelsYAML      `yaml:"models"`
	// Smoke shares the gateway's own type, so the two readers of the YAML agree on
	// the block's shape.
	Smoke config.BackendSmoke `yaml:"smoke"`
}

type modelsYAML struct {
	Allow          []string `yaml:"allow"`
	SystemOneAllow []string `yaml:"systemone_allow"`
}

type backendAuthYAML struct {
	BearerEnv string `yaml:"bearer_env"`
}

// SystemOneBackendEnabled reports whether post-deploy systemone smoke should run.
func SystemOneBackendEnabled(path string) (bool, error) {
	cfg, err := loadGatewayYAML(path)
	if err != nil {
		return false, err
	}
	if cfg.SystemOneBackend.Enabled == nil {
		return false, nil
	}
	return *cfg.SystemOneBackend.Enabled, nil
}

// RequireBackends refuses a gateway YAML with no backends, the rule the gateway
// applies when it loads the file, so a deploy stops before it starts containers.
func RequireBackends(path string) error {
	cfg, err := loadGatewayYAML(path)
	if err != nil {
		return err
	}
	for id := range cfg.Backends {
		if strings.TrimSpace(id) != "" {
			return nil
		}
	}
	return derrors.New(derrors.CodeInvalid, "gatewayconfig.RequireBackends", "config file "+path+" has no backends")
}

// SecretEnvNames returns deploy keyring env vars required for backends in gateway YAML
// (each bearer_env, each ${VAR} in a base_url, and GEMINI_API_KEY for the Gemini
// extension), appended after defaultNames (TS_AUTHKEY). Order is stable and deduplicated.
func SecretEnvNames(path string, defaultNames []string) ([]string, error) {
	cfg, err := loadGatewayYAML(path)
	if err != nil {
		return nil, err
	}
	out := append([]string(nil), defaultNames...)
	seen := make(map[string]struct{}, len(out))
	for _, n := range out {
		seen[n] = struct{}{}
	}
	// Backends in id order, so the names (and the plan that prints them) are stable.
	for _, id := range slices.Sorted(maps.Keys(cfg.Backends)) {
		for _, env := range backendSecretNames(cfg.Backends[id]) {
			appendSecret(&out, seen, env)
		}
	}
	return out, nil
}

// backendSecretNames are the env names backend b needs: its bearer_env, each ${VAR}
// in its base_url (Cloudflare's ${CF_ACCOUNT_ID}), and GEMINI_API_KEY for the Gemini
// extension. Deploy export and smoke gating both use it, so they never disagree.
func backendSecretNames(b backendYAML) []string {
	var out []string
	if env := strings.TrimSpace(b.Auth.BearerEnv); env != "" {
		out = append(out, env)
	}
	for _, m := range placeholderPattern.FindAllStringSubmatch(b.BaseURL, -1) {
		out = append(out, m[1])
	}
	if strings.EqualFold(strings.TrimSpace(b.Extension), "gemini") {
		out = append(out, "GEMINI_API_KEY")
	}
	return out
}

func appendSecret(out *[]string, seen map[string]struct{}, env string) {
	if env == "" {
		return
	}
	if _, ok := seen[env]; ok {
		return
	}
	seen[env] = struct{}{}
	*out = append(*out, env)
}

// AllowLists returns backends.*.models.allow from committed gateway YAML.
func AllowLists(path string) (map[string][]string, error) {
	cfg, err := loadGatewayYAML(path)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(cfg.Backends))
	for id, b := range cfg.Backends {
		if len(b.Models.Allow) == 0 {
			continue
		}
		out[id] = append([]string(nil), b.Models.Allow...)
	}
	return out, nil
}

func readGatewayYAML(path string) ([]byte, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is operator-supplied gateway config from deploy
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "gatewayconfig.read", "read "+path)
	}
	return data, nil
}

func loadGatewayYAML(path string) (*gatewayYAML, error) {
	data, err := readGatewayYAML(path)
	if err != nil {
		return nil, err
	}
	var cfg gatewayYAML
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "gatewayconfig.read", "parse "+path)
	}
	return &cfg, nil
}
