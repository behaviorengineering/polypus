package smoke

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/behaviorengineering/polypus/internal/clients/httpx"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// smokeHTTPClient has no Timeout: Run requires a ctx deadline, which bounds every
// probe. The transport adds a client span per request, carrying traceparent.
var smokeHTTPClient = &http.Client{Transport: observability.WrapTransport(http.DefaultTransport)}

func httpNewGet(ctx context.Context, url string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
}

func httpNewPost(ctx context.Context, url, body string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// httpDefaultDo sends a probe once through the outbound seam: no retry, so a real
// 5xx is reported rather than hidden, and no call without a deadline. The run's
// access key, when set, goes on as a bearer token.
func httpDefaultDo(req *http.Request) (*http.Response, error) {
	if key := apiKeyFrom(req.Context()); key != "" && req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return outbound.DoOnce(req.Context(), outbound.DepSmoke, smokeHTTPClient, req)
}

// apiKeyCtx carries a run's access key to every probe request built from its context.
type apiKeyCtx struct{}

func withAPIKey(ctx context.Context, key string) context.Context {
	if key = strings.TrimSpace(key); key == "" {
		return ctx
	}
	return context.WithValue(ctx, apiKeyCtx{}, key)
}

func apiKeyFrom(ctx context.Context) string {
	if key, ok := ctx.Value(apiKeyCtx{}).(string); ok {
		return key
	}
	return ""
}

// probeBodyMax caps a probe's JSON answer; speech audio gets audioBodyMax.
const (
	probeBodyMax int64 = 1 << 20
	audioBodyMax int64 = 8 << 20
	// errorBodyMax caps the answer quoted in a statusError.
	errorBodyMax = 200
)

// call sends req once and returns the body and headers of a 2xx answer, or a
// *statusError carrying the status and the start of the body. A 2xx body over limit
// bytes is an error, never a silent truncation that passes a size check or fails
// later as a parse error. what names the call in every error.
func call(req *http.Request, what string, limit int64) ([]byte, http.Header, error) {
	resp, err := httpDefaultDo(req)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", what, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: read body: %w", what, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, &statusError{what: what, status: resp.StatusCode, body: httpx.Truncate(string(raw), errorBodyMax)}
	}
	if int64(len(raw)) > limit {
		return nil, nil, fmt.Errorf("%s: body over %d bytes", what, limit)
	}
	return raw, resp.Header, nil
}

// statusError is a gateway answer outside 2xx, kept typed so callers read the status.
type statusError struct {
	what   string // the call, for example "chat".
	status int
	body   string
}

func (e *statusError) Error() string {
	if e.body == "" {
		return fmt.Sprintf("%s status %d", e.what, e.status)
	}
	return fmt.Sprintf("%s status %d: %s", e.what, e.status, e.body)
}
