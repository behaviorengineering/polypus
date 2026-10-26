package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/behaviorengineering/polypus/internal/gateway/router"
	"github.com/behaviorengineering/polypus/internal/observability"
	"go.opentelemetry.io/otel/trace"
)

type streamChunkCounterKey struct{}

type streamChunkCounter struct {
	mu     sync.Mutex
	index  int
	dumped int  // dropped-chunk dumps written for this stream.
	warned bool // a dump write failure was already logged for this stream.
	log    *slog.Logger
}

// maxChunkDumpsPerStream caps dropped-chunk dumps for one stream: an upstream that
// keeps streaming after finish_reason would otherwise write a file per chunk.
const maxChunkDumpsPerStream = 5

func withStreamChunkCounter(ctx context.Context, log *slog.Logger) context.Context {
	if ctx == nil {
		return ctx
	}
	if _, ok := ctx.Value(streamChunkCounterKey{}).(*streamChunkCounter); ok {
		return ctx
	}
	return context.WithValue(ctx, streamChunkCounterKey{}, &streamChunkCounter{log: log})
}

func nextStreamChunkIndex(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	c, ok := ctx.Value(streamChunkCounterKey{}).(*streamChunkCounter)
	if !ok || c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.index++
	return c.index
}

func recordBifrostStreamChunk(ctx context.Context, chunk router.ChatStreamChunk, action streamSSEAction, emit bool) {
	idx := nextStreamChunkIndex(ctx)
	observability.RecordChatStreamChunk(ctx, observability.StreamChunk{
		Index: idx, OpenAIJSON: chunk.OpenAIJSON, RawProviderJSON: chunk.RawProviderJSON, SSEAction: string(action), Emitted: emit,
	})
	if action == streamSSEDrop {
		dumpIllegalStreamChunk(ctx, idx, chunk.OpenAIJSON, chunk.RawProviderJSON)
	}
}

// streamCounter returns the stream's counter, or nil outside a counted stream.
func streamCounter(ctx context.Context) *streamChunkCounter {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(streamChunkCounterKey{}).(*streamChunkCounter)
	return c
}

// claimChunkDump reports whether this stream may write another dump. Only counted
// streams dump, so every dump is capped and its failure has a logger.
func claimChunkDump(c *streamChunkCounter) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dumped >= maxChunkDumpsPerStream {
		return false
	}
	c.dumped++
	return true
}

// warnChunkDumpOnce logs a failed dump write, once per stream.
func warnChunkDumpOnce(c *streamChunkCounter, path, traceID string, err error) {
	c.mu.Lock()
	already := c.warned
	c.warned = true
	c.mu.Unlock()
	if already {
		return
	}
	c.log.Warn("polypus: stream chunk dump not written", "path", path, "trace_id", traceID, "err", err)
}

func dumpIllegalStreamChunk(ctx context.Context, idx int, openaiJSON, rawProviderJSON []byte) {
	dir := observability.FailureDumpDir()
	if dir == "" {
		return
	}
	counter := streamCounter(ctx)
	if !claimChunkDump(counter) {
		return
	}
	traceID := "unknown"
	sc := trace.SpanFromContext(ctx).SpanContext()
	if sc.HasTraceID() {
		traceID = sc.TraceID().String()
	}
	name := fmt.Sprintf("stream-chunk-%s-%d-%d.json", traceID, idx, time.Now().UnixNano())
	path := filepath.Join(dir, name)
	payload := map[string]json.RawMessage{
		apiFlavorOpenAI: openaiJSON,
	}
	if len(rawProviderJSON) > 0 {
		payload["raw_provider"] = rawProviderJSON
	}
	body, err := json.Marshal(payload)
	if err == nil {
		err = os.WriteFile(path, []byte(observability.RedactDumpText(string(body))), 0o600)
	}
	if err != nil {
		warnChunkDumpOnce(counter, path, traceID, err)
	}
}
