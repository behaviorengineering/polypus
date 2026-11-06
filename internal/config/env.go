package config

import (
	"fmt"
	"os"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
)

// expandEnv replaces ${VAR} placeholders in s from the process environment.
// Substituted values are trimmed so pasted line endings do not reach URLs or headers.
// A placeholder whose variable is unset or blank is an error, so a config value
// never loads with a silent hole (".../accounts//ai/v1").
func expandEnv(s string) (string, error) {
	var missing []string
	out := os.Expand(s, func(key string) string {
		v := strings.TrimSpace(os.Getenv(key))
		if v == "" {
			missing = append(missing, key)
		}
		return v
	})
	if len(missing) > 0 {
		return "", derrors.New(derrors.CodeInvalid, "config.expandEnv", "unset environment variable(s)").
			With("vars", strings.Join(missing, ", "))
	}
	return out, nil
}

// BackendAuth holds credentials for a remote backend.
type BackendAuth struct {
	BearerEnv string `yaml:"bearer_env"`
}

// ResolveBearerToken reads the bearer token for a remote backend from the environment.
// Both failures are CodeNotReady: the operator config is incomplete, not the caller's
// request, so a gateway answers 503 rather than 400 or 500.
func (a BackendAuth) ResolveBearerToken() (string, error) {
	const op = "config.ResolveBearerToken"
	env := strings.TrimSpace(a.BearerEnv)
	if env == "" {
		return "", derrors.New(derrors.CodeNotReady, op, "auth.bearer_env required for remote backend")
	}
	token := operatorconfig.SanitizeSecret(os.Getenv(env))
	if token == "" {
		return "", derrors.New(derrors.CodeNotReady, op,
			fmt.Sprintf("environment variable %q required for remote backend", env)).With("env", env)
	}
	return token, nil
}

// EnvOr returns the trimmed value of the environment variable key, or def when it
// is unset or blank.
func EnvOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
