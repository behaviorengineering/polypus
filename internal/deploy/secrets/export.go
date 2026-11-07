// Package secrets resolves a deploy environment's secrets from the shell, its
// keyring service and its optional SOPS secrets file, writes them as dotenv files
// for compose, and checks their shape. Nothing it resolves is set in the process
// environment.
package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// DefaultNames are the secrets every deploy needs, whatever its backends: the
// Tailscale auth key. Backend keys come from the gateway YAML.
var DefaultNames = []string{"TS_AUTHKEY"}

// GatewayEnvFileName is the dotenv the gateway service loads with env_file, written
// beside outPath by Export. It holds every name except DefaultNames, so each key the
// gateway YAML names reaches the gateway container and TS_AUTHKEY does not.
const GatewayEnvFileName = ".env.gateway"

// Export resolves names for keyring service app through r (nil reads the shell, the
// OS keyring and the SOPS secrets file, and fails on a conflict) and writes them to
// outPath as dotenv, with GatewayEnvFileName beside it holding the gateway's share.
// Each name is required, and each value is checked for shape before anything is
// written.
func Export(r *Resolver, app string, names []string, outPath string) error {
	const op = "secrets.Export"
	if r == nil {
		r = &Resolver{}
	}
	if app == "" {
		app = "polypus"
	}
	if len(names) == 0 {
		names = DefaultNames
	}
	values := make(map[string]string, len(names))
	for _, n := range names {
		v, _, err := r.Resolve(app, n)
		if err != nil {
			return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeFailedPrecondition), op, "resolve "+n).
				With("keyring_service", app)
		}
		if v == "" {
			return derrors.New(derrors.CodeFailedPrecondition, op, r.missing(app, n)).With("keyring_service", app)
		}
		values[n] = v
	}
	// Check every value before writing either file, so a bad key leaves no
	// dotenv behind for compose to pick up.
	if v, ok := values[cloudflareAccountID]; ok {
		if err := validateCloudflareAccountID(app, v); err != nil {
			return derrors.Wrap(err, derrors.CodeFailedPrecondition, op, "check "+cloudflareAccountID).With("keyring_service", app)
		}
	}
	if err := validateAPIKeys(app, names, values); err != nil {
		return derrors.Wrap(err, derrors.CodeFailedPrecondition, op, "check API keys").With("keyring_service", app)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o700); err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, op, "mkdir "+filepath.Dir(outPath))
	}
	if err := writeDotenv(outPath, names, values); err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, op, "write "+outPath)
	}
	gatewayPath := filepath.Join(filepath.Dir(outPath), GatewayEnvFileName)
	if err := writeDotenv(gatewayPath, gatewayNames(names), values); err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, op, "write "+gatewayPath)
	}
	return nil
}

// gatewayNames returns names without DefaultNames, which only the tailscale
// service reads.
func gatewayNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !slices.Contains(DefaultNames, n) {
			out = append(out, n)
		}
	}
	return out
}

// writeDotenv writes names with their values to path (mode 0600), quoting a value
// that holds a space, a quote, a tab, a newline or a #.
func writeDotenv(path string, names []string, values map[string]string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("secrets: dotenv path required")
	}
	var b strings.Builder
	for _, n := range names {
		v := values[n]
		if v == "" {
			continue
		}
		if strings.ContainsAny(v, " \t\n\"'#") {
			v = fmt.Sprintf("%q", v)
		}
		b.WriteString(n + "=" + v + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("secrets: write dotenv %q: %w", path, err)
	}
	return nil
}
