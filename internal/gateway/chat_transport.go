package gateway

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/behaviorengineering/polypus/internal/upstream"
)

// proxyOrBifrostChat sends leaf chat via Bifrost when the backend is registered;
// otherwise falls back to the OpenAI HTTP proxy.
func (h chatHandler) proxyOrBifrostChat(w http.ResponseWriter, r *http.Request, backendID, downstream, backendURL string, body []byte, hop time.Duration, backendAuth string, patchThinking bool) error {
	return h.upstreams.Execute(backendID, func() error {
		if !h.router.UsesBifrost(backendID) {
			return proxyChatCompletionsOpts(w, r, backendURL, body, h.client, hop, backendAuth, patchThinking)
		}
		return h.bifrostChatResponse(w, r, backendID, downstream, body, hop, patchThinking)
	})
}

// bifrostChatResponse dials chat through Bifrost (stream or non-stream).
// patchThinking matches leaf OpenAI-compat behavior; Switchyard hops pass false.
func (h chatHandler) bifrostChatResponse(w http.ResponseWriter, r *http.Request, backendID, model string, body []byte, hop time.Duration, patchThinking bool) error {
	if patchThinking {
		if patched, ok := disableChatThinkingInRequest(body); ok {
			body = patched
		}
	}
	if chatBodyIsStream(body) {
		chunks, errCh, err := h.router.ChatCompletionStreamRaw(r.Context(), backendID, model, body, hop)
		if err != nil {
			return err
		}
		return writeBifrostChatSSE(w, chunks, errCh)
	}
	raw, err := h.router.ChatCompletionRaw(r.Context(), backendID, model, body, hop)
	if err != nil {
		return err
	}
	if fixed, changed := mergeReasoningIntoContent(raw); changed {
		raw = fixed
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(raw)))
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(raw)
	if err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	return nil
}

// writeBifrostChatSSE frames Bifrost chat chunks as OpenAI SSE.
// Headers are not committed until the first chunk (or successful empty DONE) so
// dial/stream setup errors can still map to HTTP error status.
func writeBifrostChatSSE(w http.ResponseWriter, chunks <-chan []byte, errCh <-chan error) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	started := false
	start := func() {
		if started {
			return
		}
		w.WriteHeader(http.StatusOK)
		started = true
	}
	for raw := range chunks {
		start()
		if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
			return fmt.Errorf("write stream: %w: %w", err, upstream.ErrResponseWritten)
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	if err, ok := <-errCh; ok && err != nil {
		if !started {
			return err
		}
		return fmt.Errorf("%w: %w", err, upstream.ErrResponseWritten)
	}
	start()
	if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
		return fmt.Errorf("write stream done: %w: %w", err, upstream.ErrResponseWritten)
	}
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}
