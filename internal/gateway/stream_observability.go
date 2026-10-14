package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/behaviorengineering/polypus/internal/observability"
	"go.opentelemetry.io/otel/trace"
)

type streamChunkCounterKey struct{}

type streamChunkCounter struct {
	mu    sync.Mutex
	index int
}

func withStreamChunkCounter(ctx context.Context) context.Context {
	if ctx == nil {
		return ctx
	}
	if _, ok := ctx.Value(streamChunkCounterKey{}).(*streamChunkCounter); ok {
		return ctx
	}
	return context.WithValue(ctx, streamChunkCounterKey{}, &streamChunkCounter{})
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

func recordBifrostStreamChunk(ctx context.Context, openaiJSON, rawProviderJSON, outbound []byte, action StreamSSEAction, emit bool) {
	idx := nextStreamChunkIndex(ctx)
	observability.RecordChatStreamChunk(ctx, idx, openaiJSON, rawProviderJSON, string(action), emit)
	if action == StreamSSEDrop {
		dumpIllegalStreamChunk(ctx, idx, openaiJSON, rawProviderJSON)
	}
}

func dumpIllegalStreamChunk(ctx context.Context, idx int, openaiJSON, rawProviderJSON []byte) {
	dir := observability.FailureDumpDir()
	if dir == "" {
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
		"openai": openaiJSON,
	}
	if len(rawProviderJSON) > 0 {
		payload["raw_provider"] = rawProviderJSON
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, body, 0o600)
}
