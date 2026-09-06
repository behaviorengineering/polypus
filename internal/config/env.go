package config

import (
	"fmt"
	"os"
	"strings"
)

const ExtensionCloudflare = "cloudflare"

// ExpandEnv replaces ${VAR} placeholders in s from the process environment.
func ExpandEnv(s string) string {
	return os.Expand(s, func(key string) string {
		return os.Getenv(key)
	})
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
	token := strings.TrimSpace(os.Getenv(env))
	if token == "" {
		return "", fmt.Errorf("environment variable %q required for remote backend", env)
	}
	return token, nil
}
