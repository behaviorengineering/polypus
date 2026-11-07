package gateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// splitSanitized spreads a sanitizer result into out, action and whether it emits.
func splitSanitized(r sanitizedChunk) ([]byte, streamSSEAction, bool) {
	return r.out, r.action, r.action != streamSSEDrop
}

// streamThroughGateway feeds an upstream OpenAI SSE body to writeBifrostChatSSE, one
// data frame per Bifrost chunk, and returns what the client gets. Bifrost ends a
// stream by closing its channel, so an upstream [DONE] is not a chunk. When ctx ends
// first, the chunks stop without the channel closing, as a real stream does.
func streamThroughGateway(ctx context.Context, upstream string) (*httptest.ResponseRecorder, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	chunks := make(chan router.ChatStreamChunk)
	errs := make(chan error)
	go func() {
		for _, line := range strings.Split(upstream, "\n") {
			data, ok := strings.CutPrefix(strings.TrimSpace(strings.TrimPrefix(line, "\ufeff")), "data:")
			data = strings.TrimSpace(data)
			if !ok || data == "[DONE]" {
				continue
			}
			select {
			case chunks <- router.ChatStreamChunk{OpenAIJSON: []byte(data)}:
			case <-ctx.Done():
				return
			}
		}
		close(chunks)
		close(errs)
	}()
	rec := httptest.NewRecorder()
	err := writeBifrostChatSSE(ctx, rec, bifrostStream{chunks: chunks, errs: errs})
	return rec, err
}

// gatewaySSE is the body streamThroughGateway gives the client.
func gatewaySSE(t *testing.T, upstream string) string {
	t.Helper()
	rec, err := streamThroughGateway(context.Background(), upstream)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	return rec.Body.String()
}

// choiceHasBothReasoningSpellings is the test oracle for a delta or message that
// carries both reasoning and reasoning_content.
func choiceHasBothReasoningSpellings(raw []byte) bool {
	chunk, ok := decodeChatChunk(raw)
	if !ok {
		return false
	}
	for _, ch := range chunk.choices {
		for _, field := range choiceObjectFields {
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(ch[field], &obj); err != nil {
				continue
			}
			if jsonFieldPresent(obj, keyReasoning) && jsonFieldPresent(obj, keyReasoningContent) {
				return true
			}
		}
	}
	return false
}

// mergeReasoningIntoContentJSON runs only the merge rule over a completion body.
func mergeReasoningIntoContentJSON(raw []byte) ([]byte, bool) {
	chunk, ok := decodeChatChunk(raw)
	if !ok || !mergeReasoningIntoContent(chunk.choices) {
		return raw, false
	}
	out, err := chunk.encode()
	if err != nil {
		return raw, false
	}
	return out, true
}

// stripReasoningAliasJSON runs only the alias rule over a completion body.
func stripReasoningAliasJSON(raw []byte) ([]byte, bool) {
	chunk, ok := decodeChatChunk(raw)
	if !ok || !stripDualReasoningAlias(chunk.choices) {
		return raw, false
	}
	out, err := chunk.encode()
	if err != nil {
		return raw, false
	}
	return out, true
}
