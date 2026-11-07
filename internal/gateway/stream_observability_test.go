package gateway

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

func TestStreamChunkDumpNameUsesTheGatewayClock(t *testing.T) {
	dir := t.TempDir()
	at := time.Unix(1_700_000_000, 42)
	ctx := withStreamChunkCounter(context.Background(), chunkDumpSink{dir: dir, log: testResponder.log, now: func() time.Time { return at }})
	dumpIllegalStreamChunk(ctx, 1, []byte(`{"x":1}`), nil)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("dumps %v err %v", entries, err)
	}
	if want := strconv.FormatInt(at.UnixNano(), 10) + ".json"; !strings.HasSuffix(entries[0].Name(), want) {
		t.Fatalf("name %q want suffix %q", entries[0].Name(), want)
	}
}

// chunkEventKeys records one chunk under a recording span with sink and returns the
// attribute keys of its event.
func chunkEventKeys(t *testing.T, sink chunkDumpSink) map[attribute.Key]bool {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx, span := tp.Tracer("test").Start(context.Background(), "polypus.chat")
	ctx = withStreamChunkCounter(ctx, sink)
	recordBifrostStreamChunk(ctx, router.ChatStreamChunk{OpenAIJSON: []byte(`{"id":"c1"}`)}, streamSSEForward, true)
	span.End()
	keys := map[attribute.Key]bool{}
	for _, sp := range exporter.GetSpans() {
		for _, ev := range sp.Events {
			for _, a := range ev.Attributes {
				keys[a.Key] = true
			}
		}
	}
	if !keys["polypus.stream.chunk_index"] {
		t.Fatalf("no stream chunk event recorded: %v", keys)
	}
	return keys
}

func TestStreamChunkPreviewFollowsTheGatewayOption(t *testing.T) {
	if keys := chunkEventKeys(t, chunkDumpSink{}); keys["polypus.stream.openai_preview"] {
		t.Fatal("previews are off by default")
	}
	if keys := chunkEventKeys(t, chunkDumpSink{preview: true}); !keys["polypus.stream.openai_preview"] {
		t.Fatal("WithStreamChunkDebug(true) must add the redacted preview")
	}
	var ho handlerOptions
	WithStreamChunkDebug(true)(&ho)
	if !ho.streamChunkPreview {
		t.Fatal("WithStreamChunkDebug must set the option")
	}
}
