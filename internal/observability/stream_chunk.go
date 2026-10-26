package observability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const streamChunkPreviewMax = 2048

// StreamChunk is one streamed chat chunk and what the gateway did with it.
type StreamChunk struct {
	Index           int
	OpenAIJSON      []byte
	RawProviderJSON []byte
	// SSEAction is what the gateway did with the chunk (for example emit or drop).
	SSEAction string
	Emitted   bool
}

// RecordChatStreamChunk adds a compact OpenInference event on the active span.
func RecordChatStreamChunk(ctx context.Context, c StreamChunk) {
	span := trace.SpanFromContext(ctx)
	if span == nil || !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.Int("polypus.stream.chunk_index", c.Index),
		attribute.String("polypus.stream.sse_action", strings.TrimSpace(c.SSEAction)),
		attribute.Bool("polypus.stream.emit", c.Emitted),
		attribute.Bool("polypus.stream.raw_present", len(c.RawProviderJSON) > 0),
		attribute.String("polypus.stream.openai_sha256", sha256Hex(c.OpenAIJSON)),
	}
	if len(c.RawProviderJSON) > 0 {
		attrs = append(attrs, attribute.String("polypus.stream.raw_sha256", sha256Hex(c.RawProviderJSON)))
	}
	if debugStreamChunks() {
		if p := cappedRedacted(string(c.OpenAIJSON)); p != "" {
			attrs = append(attrs, attribute.String("polypus.stream.openai_preview", p))
		}
		if len(c.RawProviderJSON) > 0 {
			if p := cappedRedacted(string(c.RawProviderJSON)); p != "" {
				attrs = append(attrs, attribute.String("polypus.stream.raw_preview", p))
			}
		}
	}
	span.AddEvent("polypus.bifrost.stream_chunk", trace.WithAttributes(attrs...))
}

func debugStreamChunks() bool {
	return config.EnvBool("POLYPUS_STREAM_CHUNK_DEBUG", false)
}

func sha256Hex(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func cappedRedacted(s string) string {
	s = redactURLsInText(s)
	if len(s) > streamChunkPreviewMax {
		s = s[:streamChunkPreviewMax]
	}
	return s
}
