package secrets

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
)

// ClientKeyName is the keyring account (and env name) of the environment's gateway
// client key, the key polypus deploy client-key creates.
const ClientKeyName = "POLYPUS_API_KEY"

// Store saves value under keyring service app, account name. A nil kr uses the OS
// keyring.
func Store(kr operatorconfig.Keyring, app, name, value string) error {
	if kr == nil {
		kr = operatorconfig.DefaultKeyring()
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("secrets: empty value for %s", name)
	}
	if err := kr.Set(app, name, value); err != nil {
		return fmt.Errorf("secrets: store %s:%s: %w", app, name, err)
	}
	return nil
}

// Lookup returns name from the environment, then keyring service app. A secret in
// neither returns "" with no error, for optional secrets such as the client key.
func Lookup(kr operatorconfig.Keyring, app, name string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v, nil
	}
	if kr == nil {
		kr = operatorconfig.DefaultKeyring()
	}
	v, err := kr.Get(app, name)
	if errors.Is(err, operatorconfig.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("secrets: read %s:%s: %w", app, name, err)
	}
	return strings.TrimSpace(v), nil
}
