// Package registry is the OCI registry client the deploy uses to check that a pinned
// image manifest exists. It sends a GitHub token to GitHub's own registry (ghcr.io)
// and to no other host: any other registry gets anonymous requests only.
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// GHCRHost is GitHub's container registry, the only host that receives the GitHub token.
const GHCRHost = "ghcr.io"

// defaultBasicUser is the GHCR token-exchange username when none is set. GitHub
// accepts any non-empty username with a PAT as the password.
const defaultBasicUser = "token"

// maxErrorBody bounds how much of an error response is kept for the message.
const maxErrorBody = 200

var manifestAccept = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

// ErrManifestNotFound means the registry has no manifest for the tag.
var ErrManifestNotFound = errors.New("registry manifest not found")

// ErrUnauthorized means the registry rejected the request or its credentials.
var ErrUnauthorized = errors.New("registry unauthorized")

// Config holds what a Client needs.
type Config struct {
	// HTTP sends the requests. Nil uses a client with an OpenTelemetry transport and
	// no timeout of its own: the caller's context deadline bounds every request.
	HTTP *http.Client
	// GitHubToken authenticates pulls from ghcr.io (private packages). It is never
	// sent to any other host.
	GitHubToken string
	// BasicUser is the username for the ghcr.io token exchange; empty uses "token".
	BasicUser string
}

// Client checks manifests in OCI registries.
type Client struct {
	http        *http.Client
	githubToken string
	basicUser   string
}

// CreateClient returns a Client for cfg.
func (cfg Config) CreateClient() *Client {
	hc := cfg.HTTP
	if hc == nil {
		hc = &http.Client{Transport: observability.WrapTransport(nil)}
	}
	user := strings.TrimSpace(cfg.BasicUser)
	if user == "" {
		user = defaultBasicUser
	}
	return &Client{http: hc, githubToken: strings.TrimSpace(cfg.GitHubToken), basicUser: user}
}

// Ref is an image reference with a tag: <registry>/<repository>:<tag>.
type Ref struct {
	Registry   string
	Repository string
	Tag        string
}

// String returns the reference as registry/repository:tag.
func (r Ref) String() string {
	return r.Registry + "/" + r.Repository + ":" + r.Tag
}

// ParseRef splits registry/repository:tag. Digest references are not supported.
func ParseRef(ref string) (Ref, error) {
	const op = "registry.ParseRef"
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Ref{}, derrors.New(derrors.CodeInvalid, op, "empty image ref")
	}
	if strings.Contains(ref, "@") {
		return Ref{}, derrors.New(derrors.CodeInvalid, op, "digest refs are not supported").With("ref", ref)
	}
	idx := strings.LastIndex(ref, ":")
	if idx <= 0 || idx == len(ref)-1 {
		return Ref{}, derrors.New(derrors.CodeInvalid, op, "missing tag").With("ref", ref)
	}
	registry, repo, ok := strings.Cut(ref[:idx], "/")
	if !ok || registry == "" || repo == "" {
		return Ref{}, derrors.New(derrors.CodeInvalid, op, "invalid image ref").With("ref", ref)
	}
	return Ref{Registry: registry, Repository: repo, Tag: ref[idx+1:]}, nil
}

// ManifestExists returns nil when the registry has a manifest for ref. It wraps
// ErrManifestNotFound (CodeNotFound) when the tag is missing and ErrUnauthorized
// (CodeUnauthorized) when the registry refuses. ctx must carry a deadline.
func (c *Client) ManifestExists(ctx context.Context, ref Ref) error {
	const op = "registry.ManifestExists"
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, op, "context required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return derrors.New(derrors.CodeFailedPrecondition, op, "context needs a deadline")
	}
	manifestURL := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme(ref.Registry), ref.Registry, ref.Repository, ref.Tag)
	status, err := c.manifestGet(ctx, ref, manifestURL, "")
	if err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "GET "+manifestURL)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		bearer, err := c.pullToken(ctx, ref)
		if err != nil {
			return derrors.Wrap(fmt.Errorf("%w: %w", ErrUnauthorized, err), derrors.CodeUnauthorized, op, c.unauthorizedHint(ref)).
				With("registry", ref.Registry)
		}
		if status, err = c.manifestGet(ctx, ref, manifestURL, bearer); err != nil {
			return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "GET "+manifestURL)
		}
	}
	return statusError(op, manifestURL, status)
}

// statusError maps a manifest GET status to nil or a typed error.
func statusError(op, manifestURL string, status int) error {
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return derrors.Wrap(fmt.Errorf("%w: %s", ErrManifestNotFound, manifestURL), derrors.CodeNotFound, op, "manifest not found")
	case http.StatusUnauthorized, http.StatusForbidden:
		return derrors.Wrap(fmt.Errorf("%w: manifest GET returned %d", ErrUnauthorized, status), derrors.CodeUnauthorized, op, "manifest refused")
	case http.StatusTooManyRequests:
		return derrors.Wrap(&StatusError{Code: status}, derrors.CodeRateLimited, op, "GET "+manifestURL)
	default:
		return derrors.Wrap(&StatusError{Code: status}, derrors.CodeUnavailable, op, "GET "+manifestURL)
	}
}

// StatusError is an unexpected registry HTTP status.
type StatusError struct {
	Code int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("registry HTTP %d", e.Code)
}

// credential is the GitHub token when ref is on ghcr.io, else empty: no other host
// ever receives it.
func (c *Client) credential(ref Ref) string {
	if !strings.EqualFold(ref.Registry, GHCRHost) {
		return ""
	}
	return c.githubToken
}

func (c *Client) unauthorizedHint(ref Ref) string {
	switch {
	case !strings.EqualFold(ref.Registry, GHCRHost):
		return "no anonymous pull token from " + ref.Registry + " (a GitHub token is only sent to " + GHCRHost + ")"
	case c.githubToken == "":
		return "no anonymous pull token; set GHCR_TOKEN or GITHUB_TOKEN with read:packages for private packages"
	default:
		return "the GitHub token was refused; it needs read:packages"
	}
}

// depName is the outbound breaker key for ref's registry.
func depName(ref Ref) string {
	if strings.EqualFold(ref.Registry, GHCRHost) {
		return outbound.DepGHCR
	}
	return outbound.Scoped(outbound.DepRegistry, ref.Registry)
}

func (c *Client) manifestGet(ctx context.Context, ref Ref, manifestURL, bearer string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return 0, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Accept", manifestAccept)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := outbound.Do(ctx, depName(ref), c.http, req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return 0, fmt.Errorf("read manifest response: %w", err)
	}
	return resp.StatusCode, nil
}

type tokenResponse struct {
	Token string `json:"token"`
}

// pullToken returns a Bearer token for a manifest GET from the registry's token
// endpoint. On ghcr.io with a GitHub token it authenticates with Basic auth, the way
// docker login does; otherwise it asks for an anonymous pull token, which is how
// public packages are read.
func (c *Client) pullToken(ctx context.Context, ref Ref) (string, error) {
	scope := fmt.Sprintf("repository:%s:pull", ref.Repository)
	tokenURL := fmt.Sprintf("%s://%s/token?service=%s&scope=%s",
		scheme(ref.Registry), ref.Registry, url.QueryEscape(ref.Registry), url.QueryEscape(scope))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("new token request: %w", err)
	}
	if pat := c.credential(ref); pat != "" {
		req.SetBasicAuth(c.basicUser, pat)
	}
	resp, err := outbound.Do(ctx, depName(ref), c.http, req)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", redactQuery(tokenURL), err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint HTTP %d: %s", resp.StatusCode, truncate(body, maxErrorBody))
	}
	var doc tokenResponse
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("token endpoint: parse JSON: %w", err)
	}
	tok := strings.TrimSpace(doc.Token)
	if tok == "" {
		return "", errors.New("token endpoint: empty token in response")
	}
	return tok, nil
}

// scheme is plain http for a registry on this machine, else https.
func scheme(registry string) string {
	if strings.HasPrefix(registry, "127.0.0.1") || strings.HasPrefix(registry, "localhost") {
		return "http"
	}
	return "https"
}

func redactQuery(raw string) string {
	base, _, _ := strings.Cut(raw, "?")
	return base
}

func truncate(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
