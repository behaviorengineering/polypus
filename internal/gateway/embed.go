package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/outbound"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
)

const embedMaxBody = 8 << 20

func extractEmbedModel(body []byte) (string, error) {
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", derrors.Wrap(err, derrors.CodeInvalid, "gateway.extractEmbedModel", "invalid json")
	}
	model := strings.TrimSpace(payload.Model)
	if model == "" {
		return "", derrors.New(derrors.CodeInvalid, "gateway.extractEmbedModel", "model required")
	}
	return model, nil
}

func rewriteEmbedModel(body []byte, model string) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "gateway.rewriteEmbedModel", "invalid json")
	}
	payload[keyModel] = model
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "gateway.rewriteEmbedModel", "marshal")
	}
	return out, nil
}

func proxyEmbeddings(w http.ResponseWriter, r *http.Request, backendURL string, body []byte, client *http.Client, hopTimeout time.Duration, backendAuth string) error {
	target := embeddingsURL(backendURL)
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

	for k, vals := range resp.Header {
		if len(vals) == 0 {
			continue
		}
		switch strings.ToLower(k) {
		case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
			"te", "trailers", "transfer-encoding", "upgrade", "content-length":
			continue
		}
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
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
