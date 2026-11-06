package gateway

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/outbound"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
)

const embedMaxBody = 8 << 20

// embedCall is one embeddings request for a resolved backend.
type embedCall struct {
	target backendTarget
	body   []byte
	hop    time.Duration
}

func (h chatHandler) proxyEmbeddings(w http.ResponseWriter, r *http.Request, c embedCall) error {
	const op = "gateway.proxyEmbeddings"
	target := embeddingsURL(c.target.url)
	hop := hopBudget(c.hop, config.DefaultTimeouts().Embed)
	ctx, cancel := context.WithTimeout(r.Context(), hop)
	defer cancel()
	req, err := newBackendPOST(ctx, r, target, c.target.auth, "application/json", c.body)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, op, "request")
	}
	req.Header.Set(config.TimeoutHeader, hop.String())
	resp, err := h.transport.policies.DoOnce(ctx, outbound.DepGateway, h.transport.client, req)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, op, "post").
			With("target", target)
	}
	defer func() { _ = resp.Body.Close() }()
	// Read before the headers go out, so an oversized reply becomes an error status
	// rather than a truncated 200.
	raw, err := readUpstream(resp.Body, embedMaxBody, "gateway.proxyEmbeddings")
	if err != nil {
		return err
	}

	copyChatResponseHeaders(w, resp.Header)

	w.WriteHeader(resp.StatusCode)
	if _, err := w.Write(raw); err != nil {
		return afterHeaders(r.Context(), err, true, "gateway.proxyEmbeddings", "write response")
	}
	return upstream.StatusFailure(resp.StatusCode)
}

func embeddingsURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if isOpenAICompatAPIRoot(base) {
		return base + "/embeddings"
	}
	return base + "/v1/embeddings"
}
