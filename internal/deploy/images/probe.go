package images

import (
	"context"
	"os"
	"strings"

	"github.com/behaviorengineering/polypus/internal/clients/registry"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// ErrManifestNotFound means the registry has no manifest for the pinned tag.
var ErrManifestNotFound = registry.ErrManifestNotFound

// ErrRegistryUnauthorized means the registry rejected the request or its credentials.
var ErrRegistryUnauthorized = registry.ErrUnauthorized

// ProbeManifest checks that ref exists in its registry. token (else GHCR_TOKEN,
// GITHUB_TOKEN or GH_TOKEN) is a GitHub token, sent to ghcr.io only.
func ProbeManifest(ctx context.Context, ref string, token string) error {
	if token == "" {
		token = registryToken()
	}
	return probeWith(ctx, newRegistryClient(token), ref)
}

// newRegistryClient is the registry client for a GitHub token from the operator's
// environment.
func newRegistryClient(token string) *registry.Client {
	return registry.Config{GitHubToken: token, BasicUser: registryBasicUser()}.CreateClient()
}

func probeWith(ctx context.Context, reg *registry.Client, ref string) error {
	const op = "images.ProbeManifest"
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, op, "context required")
	}
	parsed, err := registry.ParseRef(ref)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInvalid, op, "parse image ref")
	}
	if err := reg.ManifestExists(ctx, parsed); err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "probe "+ref)
	}
	return nil
}

// registryToken is the operator's GitHub token from the environment.
func registryToken() string {
	for _, key := range []string{"GHCR_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

// registryBasicUser is the username for GHCR PAT Basic auth (the password is the
// PAT), as with docker login ghcr.io -u <user>. Empty lets the client use its default.
func registryBasicUser() string {
	for _, key := range []string{"GHCR_USERNAME", "GITHUB_ACTOR"} {
		if u := strings.TrimSpace(os.Getenv(key)); u != "" {
			return u
		}
	}
	return ""
}
