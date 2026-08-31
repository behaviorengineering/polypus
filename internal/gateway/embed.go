package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/upstream"
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
	payload["model"] = model
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "gateway.rewriteEmbedModel", "marshal")
	}
	return out, nil
}

func proxyEmbeddings(w http.ResponseWriter, r *http.Request, backendURL string, body []byte, client *http.Client, hopTimeout time.Duration, backendAuth string) error {
	target := embeddingsURL(backendURL)
	ctx := r.Context()
	if hopTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, hopTimeout)
		defer cancel()
	}
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
	if hopTimeout > 0 {
		req.Header.Set(config.TimeoutHeader, hopTimeout.String())
	}

	if client == nil {
		client = newChatProxyClient(0)
	}
	resp, err := client.Do(req)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "gateway.proxyEmbeddings", "post").
			With("target", target)
	}
	defer func() { _ = resp.Body.Close() }()

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
	_, err = io.Copy(w, io.LimitReader(resp.Body, embedMaxBody))
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "gateway.proxyEmbeddings", "write response")
	}
	return upstream.StatusFailure(resp.StatusCode)
}

func embeddingsURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/embeddings"
	}
	return base + "/v1/embeddings"
}
