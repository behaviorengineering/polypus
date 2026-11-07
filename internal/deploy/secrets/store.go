package secrets

import (
	"strings"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// ClientKeyName is the keyring account (and env name) of the environment's gateway
// client key, the key polypus deploy client-key creates.
const ClientKeyName = "POLYPUS_API_KEY"

// Store saves value under keyring service app, account name. A nil kr uses the OS
// keyring.
func Store(kr operatorconfig.Keyring, app, name, value string) error {
	const op = "secrets.Store"
	if kr == nil {
		kr = operatorconfig.DefaultKeyring()
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return derrors.New(derrors.CodeInvalid, op, "empty value for "+name)
	}
	if err := kr.Set(app, name, value); err != nil {
		return derrors.Wrap(err, derrors.CodeFailedPrecondition, op, "store "+app+":"+name)
	}
	return nil
}
