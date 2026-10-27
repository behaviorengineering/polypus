package secrets

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var accountIDRe = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

const minAPIKeyLen = 20

// ValidateCloudflareFromEnv checks CF_* after ResolveSecrets.
func ValidateCloudflareFromEnv() error {
	accountID := os.Getenv("CF_ACCOUNT_ID")
	if !accountIDRe.MatchString(accountID) {
		return fmt.Errorf("secrets: CF_ACCOUNT_ID looks wrong (length=%d; expected 32 hex characters). Use Cloudflare dashboard account ID in keyring polypus:CF_ACCOUNT_ID, not the API token", len(accountID))
	}
	apiKey := os.Getenv("CF_AI_API_KEY")
	if len(apiKey) < minAPIKeyLen {
		return fmt.Errorf("secrets: CF_AI_API_KEY looks too short (length=%d); check polypus:CF_AI_API_KEY", len(apiKey))
	}
	return nil
}

// ValidateOptionalAPIKeysFromEnv checks non-Cloudflare API keys that were exported.
func ValidateOptionalAPIKeysFromEnv(names []string) error {
	for _, name := range names {
		name = strings.TrimSpace(name)
		switch name {
		case "CF_AI_API_KEY", "CF_ACCOUNT_ID", "TS_AUTHKEY", "":
			continue
		default:
			if err := validateMinLenEnv(name); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateMinLenEnv(name string) error {
	val := os.Getenv(name)
	if len(val) < minAPIKeyLen {
		return fmt.Errorf("secrets: %s looks too short (length=%d); check polypus:%s in keyring", name, len(val), name)
	}
	return nil
}
