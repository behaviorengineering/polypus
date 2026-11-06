package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// errorBodyMax caps the upstream body quoted in an error message.
const errorBodyMax = 200

// HTTP is how a provider client sends its catalog GETs: the client, the outbound
// policies (nil: the process default) and the breaker name.
type HTTP struct {
	Client   *http.Client
	Policies *outbound.Policies
	Dep      string
}

// GetJSON GETs target through the outbound seam (transient failures retried), reads
// at most maxBody bytes, maps a non-2xx status to a domain code and decodes the body
// into out. hdr is sent as is. Errors carry op.
func GetJSON(ctx context.Context, h HTTP, op, target string, hdr http.Header, maxBody int64, out any) error {
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, op, "context required")
	}
	if h.Client == nil {
		return derrors.New(derrors.CodeInternal, op, "http client required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, op, "request")
	}
	for k, vals := range hdr {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	resp, err := h.Policies.Do(ctx, h.Dep, h.Client, req)
	if err != nil {
		outbound.CloseResponse(resp)
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "fetch")
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := ReadCapped(resp.Body, maxBody, op)
	if err != nil {
		return err
	}
	if err := StatusError(op, resp.StatusCode, raw); err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, op, "decode")
	}
	return nil
}

// StatusError maps a non-2xx upstream answer to a domain error, or returns nil for a
// 2xx. A rejected key is CodeUnauthorized; any other permanent 4xx is marked so the
// outbound policies do not retry it.
func StatusError(op string, status int, body []byte) error {
	if status >= 200 && status < 300 {
		return nil
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return derrors.New(derrors.CodeUnauthorized, op, "API key rejected").
			With("status", strconv.Itoa(status))
	}
	err := derrors.New(StatusCode(status), op, fmt.Sprintf("status %d: %s", status, Truncate(string(body), errorBodyMax))).
		With("status", strconv.Itoa(status))
	if outbound.HTTPStatusPermanent(status) {
		return outbound.Abort(err)
	}
	return err
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
