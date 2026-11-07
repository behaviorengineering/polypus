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

// chunkDumpSink is how a stream's chunks are recorded: span events, with previews
// when asked, and dumps of the chunks it drops.
type chunkDumpSink struct {
	// preview adds redacted chunk previews to the span events
	// (observability.Config.StreamChunkDebug).
	preview bool
	// dir receives dropped-chunk dumps; empty turns them off.
	dir string
	// log reports a dump that could not be written.
	log *slog.Logger
	// now stamps the dump file names.
	now func() time.Time
}

type streamChunkCounter struct {
	mu     sync.Mutex
	index  int
	dumped int  // dropped-chunk dumps written for this stream.
	warned bool // a dump write failure was already logged for this stream.
	sink   chunkDumpSink
}

// maxChunkDumpsPerStream caps dropped-chunk dumps for one stream: an upstream that
// keeps streaming after finish_reason would otherwise write a file per chunk.
const maxChunkDumpsPerStream = 5

// withStreamChunkCounter starts counting a stream's chunks. A sink with a directory
// needs a logger and a clock; without them the stream writes no dumps.
func withStreamChunkCounter(ctx context.Context, sink chunkDumpSink) context.Context {
	if ctx == nil {
		return ctx
	}
	if _, ok := ctx.Value(streamChunkCounterKey{}).(*streamChunkCounter); ok {
		return ctx
	}
	if sink.log == nil || sink.now == nil {
		sink.dir = ""
	}
	return context.WithValue(ctx, streamChunkCounterKey{}, &streamChunkCounter{sink: sink})
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
	preview := false
	if c := streamCounter(ctx); c != nil {
		preview = c.sink.preview
	}
	observability.RecordChatStreamChunk(ctx, observability.StreamChunk{
		Index: idx, OpenAIJSON: chunk.OpenAIJSON, RawProviderJSON: chunk.RawProviderJSON, SSEAction: string(action), Emitted: emit,
		Preview: preview,
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
	c.sink.log.Warn("polypus: stream chunk dump not written", "path", path, "trace_id", traceID, "err", err)
}

// chunkDumpInfix sits between the trace id and the chunk index in a dump name, so
// /debug/failures/<trace> finds a stream's chunk dumps beside its failure dump.
const chunkDumpInfix = ".stream-chunk-"

// chunkDumpName is <trace>.stream-chunk-<index>-<unix nanos>.json. The index is
// zero-padded so a directory listing sorts a stream's dumps in chunk order.
func chunkDumpName(traceID string, idx int, at time.Time) string {
	return fmt.Sprintf("%s%s%06d-%d.json", traceID, chunkDumpInfix, idx, at.UnixNano())
}

// dumpField keeps a chunk as JSON when it is valid JSON, and as text otherwise: the
// sanitizer drops an undecodable chunk after finish_reason, and json.Marshal rejects
// an invalid RawMessage, so that chunk would never be written.
func dumpField(raw []byte) any {
	if json.Valid(raw) {
		return json.RawMessage(raw)
	}
	return string(raw)
}

func dumpIllegalStreamChunk(ctx context.Context, idx int, openaiJSON, rawProviderJSON []byte) {
	counter := streamCounter(ctx)
	if counter == nil || counter.sink.dir == "" || !claimChunkDump(counter) {
		return
	}
	dir := counter.sink.dir
	traceID := "unknown"
	sc := trace.SpanFromContext(ctx).SpanContext()
	if sc.HasTraceID() {
		traceID = sc.TraceID().String()
	}
	path := filepath.Join(dir, chunkDumpName(traceID, idx, counter.sink.now()))
	payload := map[string]any{
		apiFlavorOpenAI: dumpField(openaiJSON),
	}
	if len(rawProviderJSON) > 0 {
		payload["raw_provider"] = dumpField(rawProviderJSON)
	}
	body, err := json.Marshal(payload)
	if err == nil {
		err = os.WriteFile(path, []byte(observability.RedactDumpText(string(body))), 0o600)
	}
	if err != nil {
		warnChunkDumpOnce(counter, path, traceID, err)
	}
}
