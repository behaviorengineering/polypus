package secrets

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// cloudflareAccountID is the Cloudflare account ID a Cloudflare backend's base_url
// names. It is an identifier, not a key, so it has its own format check.
const cloudflareAccountID = "CF_ACCOUNT_ID"

var accountIDRe = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

const minAPIKeyLen = 20

// ValidateCloudflareAccountIDFromEnv checks CF_ACCOUNT_ID after ResolveSecrets; app
// names the keyring service in the message.
func ValidateCloudflareAccountIDFromEnv(app string) error {
	accountID := os.Getenv(cloudflareAccountID)
	if !accountIDRe.MatchString(accountID) {
		return fmt.Errorf("secrets: %s looks wrong (length=%d; expected 32 hex characters). Use the Cloudflare dashboard account ID in keyring %s:%s, not the API token",
			cloudflareAccountID, len(accountID), app, cloudflareAccountID)
	}
	return nil
}

// ValidateAPIKeysFromEnv checks that each exported API key is long enough to be
// real. TS_AUTHKEY and CF_ACCOUNT_ID are not API keys and are skipped.
func ValidateAPIKeysFromEnv(app string, names []string) error {
	for _, name := range names {
		name = strings.TrimSpace(name)
		switch name {
		case cloudflareAccountID, "TS_AUTHKEY", "":
			continue
		}
		if val := os.Getenv(name); len(val) < minAPIKeyLen {
			return fmt.Errorf("secrets: %s looks too short (length=%d); check %s:%s in keyring", name, len(val), app, name)
		}
	}
	return nil
}
