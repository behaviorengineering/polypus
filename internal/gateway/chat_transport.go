package gateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
	"github.com/behaviorengineering/polypus/internal/observability"
)

// dialChat encodes the request for its target and sends it, answering the client
// itself on failure. A vision request gets the vision hop budget.
func (h chatHandler) dialChat(w http.ResponseWriter, r *http.Request, req chatRequest, t backendTarget) {
	body, err := req.outbound(t.downstream, t.backend.Extension)
	if err != nil {
		h.writeHandlerError(w, err)
		return
	}
	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.chat", observability.LLMSpanAttrs{
		Model: req.model, BackendID: t.id, BackendURL: t.url, Downstream: t.downstream,
	})
	defer func() { endDialSpan(span, err, t.id) }()
	r = r.WithContext(ctx)
	hop := h.timeouts.ResolveChat(r.Header.Get(config.TimeoutHeader), t.id, config.ChatKindOf(req.vision, req.thinking))
	c := chatCall{target: t, body: body, stream: req.stream, hop: hop}
	if err = h.requireBifrost("gateway.dialChat", t.id); err == nil {
		err = h.upstreams.Execute(t.id, func() error { return h.bifrostChat(w, r, c) })
	}
	if err != nil {
		h.writeUpstreamDialError(w, dialFailure{err: err, upstream: t.id})
	}
}

// chatCall is one encoded chat request for a resolved backend.
type chatCall struct {
	target backendTarget
	body   []byte
	stream bool
	hop    time.Duration
	// recordRouterSelectedModel tags the span with the model a composed router
	// picked and passes its selected-model headers to the caller.
	recordRouterSelectedModel bool
}

// requireBifrost fails closed when backendID is not registered with the router:
// chat and embeddings go through Bifrost only, with no HTTP fallback.
func (s *shared) requireBifrost(op, backendID string) error {
	if s.chat.UsesBifrost(backendID) {
		return nil
	}
	return derrors.New(derrors.CodeFailedPrecondition, op, "backend not registered with the router").
		With("backend", backendID)
}

// bifrostChat sends one chat call through Bifrost, streamed or not. The caller has
// checked the backend is registered and runs it under the backend's breaker.
func (h chatHandler) bifrostChat(w http.ResponseWriter, r *http.Request, c chatCall) error {
	if c.stream {
		return h.bifrostChatStream(w, r, c)
	}
	return h.bifrostChatResponse(w, r, c)
}

// routerCall is the router call for c. A composed router's pick is recorded from
// the provider's response headers through sel (nil records nothing).
func (c chatCall) routerCall(w http.ResponseWriter, sel *selectedModel, timeout time.Duration) router.Call {
	call := router.Call{BackendID: c.target.id, Model: c.target.downstream, Body: c.body, Timeout: timeout}
	if sel != nil {
		call.OnResponseHeader = func(h http.Header) { sel.fromHeader(w, h) }
	}
	return call
}

// selection returns the recorder of a composed router's pick, or nil for a leaf call.
func (c chatCall) selection(ctx context.Context) *selectedModel {
	if !c.recordRouterSelectedModel {
		return nil
	}
	return &selectedModel{ctx: ctx}
}

// bifrostChatStream dials a chat stream through Bifrost: the hop bounds the wait for
// the first chunk and the idle limit each gap after it.
func (h chatHandler) bifrostChatStream(w http.ResponseWriter, r *http.Request, c chatCall) error {
	const op = "gateway.bifrostChatStream"
	backendID := c.target.id
	streamCtx, cancel := context.WithCancel(r.Context())
	defer cancel()
	hop := hopBudget(c.hop, config.DefaultTimeouts().Chat)
	wd := startStreamWatchdog(hop, h.timeouts.ResolveStreamIdle(backendID), cancel)
	defer wd.stop()
	sel := c.selection(r.Context())
	chunks, errCh, err := h.chat.ChatCompletionStreamRaw(streamCtx, c.routerCall(w, sel, 0))
	if err != nil {
		if wd.timedOut() {
			return derrors.Wrap(err, derrors.CodeTimeout, op, "no stream within the hop budget").
				With("backend", backendID).With("hop", hop.String())
		}
		return gatewayErr(err, op, "stream").With("backend", backendID)
	}
	ctx := withStreamChunkCounter(streamCtx, h.chunkDumps())
	return writeBifrostChatSSE(ctx, w, bifrostStream{chunks: chunks, errs: errCh, watchdog: wd, selected: sel})
}

// bifrostChatResponse dials a non-stream chat through Bifrost. The hop bounds the
// whole call.
func (h chatHandler) bifrostChatResponse(w http.ResponseWriter, r *http.Request, c chatCall) error {
	backendID := c.target.id
	sel := c.selection(r.Context())
	hop := hopBudget(c.hop, config.DefaultTimeouts().Chat)
	raw, err := h.chat.ChatCompletionRaw(r.Context(), c.routerCall(w, sel, hop))
	if err != nil {
		return gatewayErr(err, "gateway.bifrostChatResponse", "chat").With("backend", backendID)
	}
	raw = normalizeChatCompletion(r.Context(), w, raw)
	sel.fromBody(raw)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(raw)
	if err != nil {
		return afterHeaders(r.Context(), err, true, "gateway.bifrostChatResponse", "write response")
	}
	return nil
}

// streamWatchdog cancels a stream that waits too long: for its first chunk (the hop
// budget), then between chunks (the idle limit; zero or less turns it off).
type streamWatchdog struct {
	timer *time.Timer
	idle  time.Duration
	fired atomic.Bool
}

func startStreamWatchdog(hop, idle time.Duration, cancel context.CancelFunc) *streamWatchdog {
	wd := &streamWatchdog{idle: idle}
	wd.timer = time.AfterFunc(hop, func() {
		wd.fired.Store(true)
		cancel()
	})
	return wd
}

// chunk records a chunk: the next one must arrive within the idle limit.
func (wd *streamWatchdog) chunk() {
	if wd == nil {
		return
	}
	if wd.idle <= 0 {
		wd.timer.Stop()
		return
	}
	wd.timer.Reset(wd.idle)
}

// timedOut reports whether the watchdog, not the client, ended the stream.
func (wd *streamWatchdog) timedOut() bool {
	return wd != nil && wd.fired.Load()
}

func (wd *streamWatchdog) stop() {
	if wd != nil {
		wd.timer.Stop()
	}
}

// bifrostStream is one Bifrost chat stream: its chunks, its terminal error, the
// watchdog bounding it (nil for no bound) and the recorder of a composed router's
// pick (nil for a leaf stream).
type bifrostStream struct {
	chunks   <-chan router.ChatStreamChunk
	errs     <-chan error
	watchdog *streamWatchdog
	selected *selectedModel
}

// writeBifrostChatSSE frames Bifrost chat chunks as OpenAI SSE.
// Mid-stream choices that omit finish_reason get "finish_reason":null so
// OpenAI-compat clients that require the key (for example llm-go) can parse.
// Headers are not committed until the first chunk (or successful empty DONE) so
// dial/stream setup errors can still map to HTTP error status.
func writeBifrostChatSSE(ctx context.Context, w http.ResponseWriter, stream bifrostStream) error {
	const op = "gateway.writeBifrostChatSSE"
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, op, "context required")
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
	for {
		var chunk router.ChatStreamChunk
		var open bool
		select {
		case <-ctx.Done():
			return streamEnded(ctx, stream.watchdog, started)
		case chunk, open = <-stream.chunks:
		}
		if !open {
			break
		}
		stream.watchdog.chunk()
		stream.selected.fromBody(chunk.OpenAIJSON)
		res := sanitizeOpenAIStreamChunk(chunk.OpenAIJSON, san, true)
		if res.alias {
			setIntervention(ctx, w, observability.InterventionReasoningAlias)
		}
		recordBifrostStreamChunk(ctx, chunk, res.action, res.action != streamSSEDrop)
		if res.action == streamSSEDrop {
			setIntervention(ctx, w, observability.InterventionSSEDrop)
			continue
		}
		start()
		if _, err := fmt.Fprintf(w, "data: %s\n\n", res.out); err != nil {
			return afterHeaders(ctx, err, true, op, "write stream")
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	if err, ok := <-stream.errs; ok && err != nil {
		if stream.watchdog.timedOut() {
			return streamEnded(ctx, stream.watchdog, started)
		}
		if !started {
			return gatewayErr(err, op, "stream setup")
		}
		return afterHeaders(ctx, err, false, op, "stream error after headers")
	}
	start()
	if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
		return afterHeaders(ctx, err, true, op, "write stream done")
	}
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}

// streamEnded maps a stream whose context ended. A watchdog timeout is the backend
// stalling, so it counts against the breaker (CodeTimeout); otherwise the client went.
func streamEnded(ctx context.Context, wd *streamWatchdog, started bool) error {
	const op = "gateway.writeBifrostChatSSE"
	if wd.timedOut() {
		msg := "no stream chunk within the hop budget"
		if started {
			msg = "stream stalled past the idle limit"
		}
		err := derrors.Wrap(ctx.Err(), derrors.CodeTimeout, op, msg)
		if started {
			return fmt.Errorf("%w: %w", err, upstream.ErrResponseWritten)
		}
		return err
	}
	if started {
		return afterHeaders(ctx, ctx.Err(), true, op, "context done")
	}
	return derrors.Wrap(ctx.Err(), derrors.CodeCanceled, op, "context done")
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
