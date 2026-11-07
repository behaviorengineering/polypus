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
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/behaviorengineering/polypus/internal/clients/httpx"
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

// maxManifestDrain bounds how much of a manifest answer is drained; only its status
// is read.
const maxManifestDrain = 1 << 20

// maxTokenBody bounds a token endpoint answer.
const maxTokenBody = 1 << 20

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
	// Policies holds the retry and per-host breaker state; nil uses the process
	// default, which every client in one CLI process shares.
	Policies *outbound.Policies
}

// Client checks manifests in OCI registries.
type Client struct {
	http        *http.Client
	githubToken string
	basicUser   string
	policies    *outbound.Policies
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
	return &Client{http: hc, githubToken: strings.TrimSpace(cfg.GitHubToken), basicUser: user, policies: cfg.Policies}
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
// (CodeUnauthorized) when the registry refuses. A token endpoint that is down, slow
// or rate limited keeps its own code, so an outage never reads as a refused token.
// ctx must carry a deadline.
func (c *Client) ManifestExists(ctx context.Context, ref Ref) error {
	const op = "registry.ManifestExists"
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, op, "context required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return derrors.New(derrors.CodeFailedPrecondition, op, "context needs a deadline")
	}
	manifestURL := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme(ref.Registry), ref.Registry, ref.Repository, ref.Tag)
	ans, err := c.manifestGet(ctx, ref, manifestURL, "")
	if err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "GET "+manifestURL)
	}
	status := ans.status
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		bearer, err := c.pullToken(ctx, ref, ans.challenge)
		if err != nil {
			if code := derrors.CodeOr(err, derrors.CodeUnauthorized); code != derrors.CodeUnauthorized {
				// Transport, deadline, 429 or 5xx from the token endpoint: keep its code.
				return derrors.Wrap(err, code, op, "pull token").With("registry", ref.Registry)
			}
			return derrors.Wrap(fmt.Errorf("%w: %w", ErrUnauthorized, err), derrors.CodeUnauthorized, op, c.unauthorizedHint(ref)).
				With("registry", ref.Registry)
		}
		if ans, err = c.manifestGet(ctx, ref, manifestURL, bearer); err != nil {
			return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "GET "+manifestURL)
		}
		status = ans.status
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

// credential is the GitHub token when ref is on ghcr.io and tokenURL is ghcr.io's own
// token service, else empty: no other host ever receives it, even when a ghcr.io
// challenge names another realm.
func (c *Client) credential(ref Ref, tokenURL *url.URL) string {
	if !strings.EqualFold(ref.Registry, GHCRHost) || !strings.EqualFold(tokenURL.Host, GHCRHost) {
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

// depName is the outbound breaker key for a registry or token host.
func depName(host string) string {
	if strings.EqualFold(host, GHCRHost) {
		return outbound.DepGHCR
	}
	return outbound.Scoped(outbound.DepRegistry, host)
}

// manifestAnswer is what a manifest GET tells the caller: its status and, on a 401,
// the WWW-Authenticate challenge naming the token service.
type manifestAnswer struct {
	status    int
	challenge string
}

func (c *Client) manifestGet(ctx context.Context, ref Ref, manifestURL, bearer string) (manifestAnswer, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return manifestAnswer{}, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Accept", manifestAccept)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.policies.Do(ctx, depName(ref.Registry), c.http, req)
	if err != nil {
		outbound.CloseResponse(resp)
		return manifestAnswer{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	// Only the status matters; drain a bounded part so the connection can be reused,
	// and never stream an unbounded body until the deadline.
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, maxManifestDrain)); err != nil {
		return manifestAnswer{}, fmt.Errorf("read manifest response: %w", err)
	}
	return manifestAnswer{status: resp.StatusCode, challenge: resp.Header.Get("WWW-Authenticate")}, nil
}

type tokenResponse struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
}

// pullToken returns a Bearer token for a manifest GET. It asks the token service the
// registry's challenge names (realm, service and scope), or <registry>/token when the
// challenge names none. On ghcr.io with a GitHub token it authenticates with Basic
// auth, the way docker login does; otherwise it asks for an anonymous pull token,
// which is how public packages are read. A refusal is CodeUnauthorized; a transport
// failure, deadline, 429 or 5xx keeps its own code.
func (c *Client) pullToken(ctx context.Context, ref Ref, challenge string) (string, error) {
	const op = "registry.pullToken"
	tokenURL, err := tokenEndpoint(ref, challenge)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL.String(), nil)
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeInternal, op, "new token request")
	}
	if pat := c.credential(ref, tokenURL); pat != "" {
		req.SetBasicAuth(c.basicUser, pat)
	}
	target := redactQuery(tokenURL.String())
	resp, err := c.policies.Do(ctx, depName(tokenURL.Host), c.http, req)
	if err != nil {
		outbound.CloseResponse(resp)
		return "", derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "GET "+target)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadCapped(resp.Body, maxTokenBody, op)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", derrors.New(tokenStatusCode(resp.StatusCode), op,
			fmt.Sprintf("token endpoint HTTP %d: %s", resp.StatusCode, httpx.Truncate(string(body), maxErrorBody))).
			With("status", strconv.Itoa(resp.StatusCode)).With("target", target)
	}
	var doc tokenResponse
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", derrors.Wrap(err, derrors.CodeUnavailable, op, "parse token response").With("target", target)
	}
	tok := strings.TrimSpace(doc.Token)
	if tok == "" {
		tok = strings.TrimSpace(doc.AccessToken)
	}
	if tok == "" {
		return "", derrors.New(derrors.CodeUnavailable, op, "empty token in response").With("target", target)
	}
	return tok, nil
}

// tokenStatusCode maps a token endpoint status: a 429 is a rate limit and a 5xx an
// outage; any other refusal (401, 403, 404, another 4xx) means no token.
func tokenStatusCode(status int) derrors.Code {
	switch {
	case status == http.StatusTooManyRequests:
		return derrors.CodeRateLimited
	case status >= 500:
		return derrors.CodeUnavailable
	default:
		return derrors.CodeUnauthorized
	}
}

// tokenEndpoint is the token service URL for ref: the realm of the registry's Bearer
// challenge with its service and scope, or <registry>/token when the challenge names
// no realm. A realm must be https (plain http only on this machine).
func tokenEndpoint(ref Ref, challenge string) (*url.URL, error) {
	const op = "registry.tokenEndpoint"
	scope := fmt.Sprintf("repository:%s:pull", ref.Repository)
	params := parseBearerChallenge(challenge)
	realm := params["realm"]
	if realm == "" {
		u, err := url.Parse(fmt.Sprintf("%s://%s/token", scheme(ref.Registry), ref.Registry))
		if err != nil {
			return nil, derrors.Wrap(err, derrors.CodeInvalid, op, "token url").With("registry", ref.Registry)
		}
		u.RawQuery = url.Values{"service": {ref.Registry}, "scope": {scope}}.Encode()
		return u, nil
	}
	u, err := url.Parse(realm)
	if err != nil || u.Host == "" || (u.Scheme != "https" && (u.Scheme != "http" || !isLocalHost(u.Hostname()))) {
		return nil, derrors.New(derrors.CodeUnauthorized, op, "token realm is not an https URL").
			With("registry", ref.Registry).With("realm", redactQuery(realm))
	}
	q := u.Query()
	if svc := params["service"]; svc != "" {
		q.Set("service", svc)
	}
	if sc := params["scope"]; sc != "" {
		scope = sc
	}
	q.Set("scope", scope)
	u.RawQuery = q.Encode()
	return u, nil
}

// parseBearerChallenge reads the parameters of a WWW-Authenticate Bearer challenge,
// such as Bearer realm="https://auth.docker.io/token",service="registry.docker.io".
// Keys are lower case; any other scheme yields none.
func parseBearerChallenge(h string) map[string]string {
	scheme, rest, ok := strings.Cut(strings.TrimSpace(h), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return nil
	}
	params := map[string]string{}
	for rest = strings.TrimSpace(rest); rest != ""; {
		key, after, ok := strings.Cut(rest, "=")
		if !ok {
			break
		}
		after = strings.TrimSpace(after)
		var val string
		if strings.HasPrefix(after, `"`) {
			end := strings.IndexByte(after[1:], '"')
			if end < 0 {
				break
			}
			val, after = after[1:1+end], after[2+end:]
		} else {
			val, after, _ = strings.Cut(after, ",")
		}
		params[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(val)
		rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(after), ","))
	}
	return params
}

// isLocalHost reports this machine: localhost or a loopback address.
func isLocalHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
