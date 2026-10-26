package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
)

const (
	ExtensionCloudflare = "cloudflare"
	ExtensionGemini     = "gemini"
	ExtensionAnthropic  = "anthropic"
)

// ExpandEnv replaces ${VAR} placeholders in s from the process environment.
// Substituted values are trimmed so pasted line endings do not reach URLs or headers.
// A placeholder whose variable is unset or blank is an error, so a config value
// never loads with a silent hole (".../accounts//ai/v1").
func ExpandEnv(s string) (string, error) {
	var missing []string
	out := os.Expand(s, func(key string) string {
		v := strings.TrimSpace(os.Getenv(key))
		if v == "" {
			missing = append(missing, key)
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("unset environment variable(s) %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// BackendAuth holds credentials for a remote backend.
type BackendAuth struct {
	BearerEnv string `yaml:"bearer_env"`
}

// ResolveBearerToken reads the bearer token for a remote backend from the environment.
func (a BackendAuth) ResolveBearerToken() (string, error) {
	env := strings.TrimSpace(a.BearerEnv)
	if env == "" {
		return "", fmt.Errorf("auth.bearer_env required for remote backend")
	}
	token := operatorconfig.SanitizeSecret(os.Getenv(env))
	if token == "" {
		return "", fmt.Errorf("environment variable %q required for remote backend", env)
	}
	return token, nil
}
