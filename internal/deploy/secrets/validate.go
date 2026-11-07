package secrets

import (
	"fmt"
	"regexp"
	"strings"
)

// cloudflareAccountID is the Cloudflare account ID a Cloudflare backend's base_url
// names. It is an identifier, not a key, so it has its own format check.
const cloudflareAccountID = "CF_ACCOUNT_ID"

var accountIDRe = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

const minAPIKeyLen = 20

// validateCloudflareAccountID checks the resolved CF_ACCOUNT_ID; app names the
// keyring service in the message.
func validateCloudflareAccountID(app, accountID string) error {
	if !accountIDRe.MatchString(accountID) {
		return fmt.Errorf("secrets: %s looks wrong (length=%d; expected 32 hex characters). Use the Cloudflare dashboard account ID in keyring %s:%s, not the API token",
			cloudflareAccountID, len(accountID), app, cloudflareAccountID)
	}
	return nil
}

// validateAPIKeys checks that each resolved API key in names is long enough to be
// real. TS_AUTHKEY and CF_ACCOUNT_ID are not API keys and are skipped.
func validateAPIKeys(app string, names []string, values map[string]string) error {
	for _, name := range names {
		name = strings.TrimSpace(name)
		switch name {
		case cloudflareAccountID, "TS_AUTHKEY", "":
			continue
		}
		if val := values[name]; len(val) < minAPIKeyLen {
			return fmt.Errorf("secrets: %s looks too short (length=%d); check %s:%s in keyring", name, len(val), app, name)
		}
	}
	return nil
}
