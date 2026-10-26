package gateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
)

// proxyOrBifrostChat sends leaf chat via Bifrost when the backend is registered;
// otherwise falls back to the OpenAI HTTP proxy.
func (h chatHandler) proxyOrBifrostChat(w http.ResponseWriter, r *http.Request, backendID, downstream, backendURL string, body []byte, hop time.Duration, backendAuth string, patchThinking bool) error {
	thinkingExtension := ""
	if patchThinking {
		if b, ok := h.router.Registry().Backend(backendID); ok {
			thinkingExtension = b.Extension
		}
		if patched, ok := applyChatThinking(body, thinkingExtension, downstream); ok {
			body = patched
		}
	}
	return h.upstreams.Execute(backendID, func() error {
		if !h.router.UsesBifrost(backendID) {
			return proxyChat(w, r, body, chatDial{
				backendURL:    backendURL,
				client:        h.client,
				hop:           hop,
				streamIdle:    h.timeouts.ResolveStreamIdle(backendID),
				backendAuth:   backendAuth,
				thinkingModel: downstream,
			})
		}
		return h.bifrostChatResponse(w, r, backendID, downstream, body, hop)
	})
}

// bifrostChatResponse dials chat through Bifrost (stream or non-stream).
func (h chatHandler) bifrostChatResponse(w http.ResponseWriter, r *http.Request, backendID, model string, body []byte, hop time.Duration) error {
	if chatBodyIsStream(body) {
		chunks, errCh, err := h.router.ChatCompletionStreamRaw(r.Context(), backendID, model, body)
		if err != nil {
			return gatewayErr(err, "gateway.bifrostChatResponse", "stream").With("backend", backendID)
		}
		ctx := withStreamChunkCounter(r.Context())
		return writeBifrostChatSSE(ctx, w, chunks, errCh)
	}
	raw, err := h.router.ChatCompletionRaw(r.Context(), backendID, model, body, hop)
	if err != nil {
		return gatewayErr(err, "gateway.bifrostChatResponse", "chat").With("backend", backendID)
	}
	if fixed, changed := mergeReasoningIntoContent(raw); changed {
		raw = fixed
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(raw)))
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(raw)
	if err != nil {
		return afterHeaders(r.Context(), err, true, "gateway.bifrostChatResponse", "write response")
	}
	return nil
}

// writeBifrostChatSSE frames Bifrost chat chunks as OpenAI SSE.
// Mid-stream choices that omit finish_reason get "finish_reason":null so
// OpenAI-compat clients that require the key (for example llm-go) can parse.
// Headers are not committed until the first chunk (or successful empty DONE) so
// dial/stream setup errors can still map to HTTP error status.
func writeBifrostChatSSE(ctx context.Context, w http.ResponseWriter, chunks <-chan router.ChatStreamChunk, errCh <-chan error) error {
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, "gateway.writeBifrostChatSSE", "context required")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	san := newOpenAIStreamSanitizer()
	started := false
	start := func() {
		if started {
			return
		}
		w.WriteHeader(http.StatusOK)
		started = true
	}
	for chunk := range chunks {
		if ctx.Err() != nil {
			if started {
				return afterHeaders(ctx, ctx.Err(), true, "gateway.writeBifrostChatSSE", "context done")
			}
			return derrors.Wrap(ctx.Err(), derrors.CodeCanceled, "gateway.writeBifrostChatSSE", "context done")
		}
		start()
		raw := chunk.OpenAIJSON
		out, action, emit := sanitizeOpenAIStreamChunk(raw, san, true)
		recordBifrostStreamChunk(ctx, chunk.OpenAIJSON, chunk.RawProviderJSON, out, action, emit)
		if !emit {
			continue
		}
		raw = out
		if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
			return afterHeaders(ctx, err, true, "gateway.writeBifrostChatSSE", "write stream")
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	if err, ok := <-errCh; ok && err != nil {
		if !started {
			return gatewayErr(err, "gateway.writeBifrostChatSSE", "stream setup")
		}
		return afterHeaders(ctx, err, false, "gateway.writeBifrostChatSSE", "stream error after headers")
	}
	start()
	if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
		return afterHeaders(ctx, err, true, "gateway.writeBifrostChatSSE", "write stream done")
	}
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}

// afterHeaders wraps an error raised once the response status is on the wire. It is
// tagged upstream.ErrResponseWritten so no second body is written. It is coded
// CodeCanceled when the client has gone (a write to it failed, or its request
// context ended), which the breaker ignores; otherwise CodeUnavailable, a backend
// failure.
func afterHeaders(ctx context.Context, err error, clientWrite bool, op, msg string) error {
	code := derrors.CodeUnavailable
	if clientWrite || ctx.Err() != nil {
		code = derrors.CodeCanceled
	}
	return fmt.Errorf("%w: %w", derrors.Wrap(err, code, op, msg), upstream.ErrResponseWritten)
}

// gatewayErr wraps err with a gateway op, keeping any domain code already in the
// chain (so the breaker, rate-limit and response-written checks still see it);
// an untyped cause counts as the upstream failing.
func gatewayErr(err error, op, msg string) *derrors.Error {
	return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, msg)
}
