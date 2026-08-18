package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
)

const embedMaxBody = 8 << 20

func extractEmbedModel(body []byte) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("invalid json: %w", err)
	}
	model, _ := payload["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		return "", fmt.Errorf("model required")
	}
	return model, nil
}

func rewriteEmbedModel(body []byte, model string) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}
	payload["model"] = model
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	return out, nil
}

func proxyEmbeddings(w http.ResponseWriter, r *http.Request, backendURL string, body []byte, client *http.Client, hopTimeout time.Duration) error {
	base := strings.TrimRight(strings.TrimSpace(backendURL), "/")
	target := base + "/embeddings"
	ctx := r.Context()
	if hopTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, hopTimeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if auth := r.Header.Get("Authorization"); auth != "" {
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
		return fmt.Errorf("post %s: %w", target, err)
	}
	defer resp.Body.Close()

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
		return fmt.Errorf("write response: %w", err)
	}
	return nil
}
