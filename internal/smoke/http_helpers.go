package smoke

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

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
// 5xx is reported rather than hidden, and no call without a deadline.
func httpDefaultDo(req *http.Request) (*http.Response, error) {
	return outbound.DoOnce(req.Context(), outbound.DepSmoke, smokeHTTPClient, req)
}

func ioReadAllLimit(r io.Reader, n int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, n))
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
