// Package httpx holds what the provider clients share for reading an upstream HTTP
// answer: one capped JSON GET through the outbound seam, a body read that fails
// rather than truncates, the status to domain code map, and the rune-safe text cut
// their error messages use.
package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// errorBodyMax caps the upstream body quoted in an error message.
const errorBodyMax = 200

// HTTP is how a provider client sends its GETs: the client, the outbound policies
// (nil: the process default) and the breaker name.
type HTTP struct {
	Client   *http.Client
	Policies *outbound.Policies
	Dep      string
}

// JSONGet is one capped JSON GET: the op its errors carry, the target URL, the
// headers sent as they are, and the most body bytes read.
type JSONGet struct {
	Op      string
	URL     string
	Header  http.Header
	MaxBody int64
}

// GetJSON GETs g.URL through the outbound seam (transient failures retried), reads at
// most g.MaxBody bytes, maps a non-2xx status to a domain code and decodes the body
// into a T. Errors carry g.Op.
func GetJSON[T any](ctx context.Context, h HTTP, g JSONGet) (T, error) {
	var out T
	if ctx == nil {
		return out, derrors.New(derrors.CodeInvalid, g.Op, "context required")
	}
	if h.Client == nil {
		return out, derrors.New(derrors.CodeInternal, g.Op, "http client required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.URL, nil)
	if err != nil {
		return out, derrors.Wrap(err, derrors.CodeInternal, g.Op, "request")
	}
	for k, vals := range g.Header {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	resp, err := h.Policies.Do(ctx, h.Dep, h.Client, req)
	if err != nil {
		outbound.CloseResponse(resp)
		return out, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), g.Op, "fetch")
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := ReadCapped(resp.Body, g.MaxBody, g.Op)
	if err != nil {
		return out, err
	}
	if err := statusError(g.Op, resp.StatusCode, raw); err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, derrors.Wrap(err, derrors.CodeUnavailable, g.Op, "decode")
	}
	return out, nil
}

// statusError maps a non-2xx upstream answer to a domain error, or returns nil for a
// 2xx. A rejected key is CodeUnauthorized; any other status takes StatusCode's code.
// It runs after the outbound policies have finished, so it marks nothing for retry.
func statusError(op string, status int, body []byte) error {
	if status >= 200 && status < 300 {
		return nil
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return derrors.New(derrors.CodeUnauthorized, op, "API key rejected").
			With("status", strconv.Itoa(status))
	}
	return derrors.New(StatusCode(status), op, fmt.Sprintf("status %d: %s", status, Truncate(string(body), errorBodyMax))).
		With("status", strconv.Itoa(status))
}

// ReadCapped reads at most limit bytes of an upstream body. A longer body is an
// error rather than a silent truncation that later fails as a parse error.
func ReadCapped(r io.Reader, limit int64, op string) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, op, "read body")
	}
	if int64(len(raw)) > limit {
		return nil, derrors.New(derrors.CodeUnavailable, op, "upstream response too large").
			With("limit_bytes", strconv.FormatInt(limit, 10))
	}
	return raw, nil
}

// StatusCode maps an upstream HTTP status to the closest domain code.
func StatusCode(status int) derrors.Code {
	switch {
	case status == http.StatusTooManyRequests:
		return derrors.CodeRateLimited
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return derrors.CodeUnauthorized
	case status == http.StatusNotFound || status == http.StatusGone:
		return derrors.CodeNotFound
	case status >= 400 && status < 500:
		return derrors.CodeInvalid
	default:
		return derrors.CodeUnavailable
	}
}

// Truncate trims s and caps it at n runes for error messages, ending a cut string
// with "...". It cuts on rune boundaries, so upstream text stays valid UTF-8.
func Truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:max(0, n)]) + "..."
}
