package observability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const streamChunkPreviewMax = 2048

// RecordChatStreamChunk adds a compact OpenInference event on the active span.
func RecordChatStreamChunk(ctx context.Context, chunkIndex int, openaiJSON, rawProviderJSON []byte, sseAction string, emit bool) {
	span := trace.SpanFromContext(ctx)
	if span == nil || !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.Int("polypus.stream.chunk_index", chunkIndex),
		attribute.String("polypus.stream.sse_action", strings.TrimSpace(sseAction)),
		attribute.Bool("polypus.stream.emit", emit),
		attribute.Bool("polypus.stream.raw_present", len(rawProviderJSON) > 0),
		attribute.String("polypus.stream.openai_sha256", sha256Hex(openaiJSON)),
	}
	if len(rawProviderJSON) > 0 {
		attrs = append(attrs, attribute.String("polypus.stream.raw_sha256", sha256Hex(rawProviderJSON)))
	}
	if debugStreamChunks() {
		if p := cappedRedacted(string(openaiJSON)); p != "" {
			attrs = append(attrs, attribute.String("polypus.stream.openai_preview", p))
		}
		if len(rawProviderJSON) > 0 {
			if p := cappedRedacted(string(rawProviderJSON)); p != "" {
				attrs = append(attrs, attribute.String("polypus.stream.raw_preview", p))
			}
		}
	}
	span.AddEvent("polypus.bifrost.stream_chunk", trace.WithAttributes(attrs...))
}

func debugStreamChunks() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("POLYPUS_STREAM_CHUNK_DEBUG"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
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
