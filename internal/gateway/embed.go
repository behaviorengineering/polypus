package gateway

import (
	"bytes"
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

func (h chatHandler) proxyEmbeddings(w http.ResponseWriter, r *http.Request, t backendTarget, body []byte, hopTimeout time.Duration) error {
	client, backendAuth := h.client, t.auth
	target := embeddingsURL(t.url)
	hopTimeout = hopBudget(hopTimeout, config.DefaultTimeouts().Embed)
	ctx, cancel := context.WithTimeout(r.Context(), hopTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "gateway.proxyEmbeddings", "request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if backendAuth != "" {
		req.Header.Set("Authorization", backendAuth)
	} else if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	req.Header.Set(config.TimeoutHeader, hopTimeout.String())

	if client == nil {
		client = newChatProxyClient(0)
	}
	resp, err := outbound.DoOnce(ctx, outbound.DepGateway, client, req)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "gateway.proxyEmbeddings", "post").
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
