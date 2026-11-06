package gateway

import (
	"context"
	"encoding/json"
	"io"
)

// splitSanitized spreads a sanitizer result into out, action and whether it emits.
func splitSanitized(r sanitizedChunk) ([]byte, streamSSEAction, bool) {
	return r.out, r.action, r.action != streamSSEDrop
}

// copyOpenAIChatStream sanitizes a stream that is not the HTTP response.
func copyOpenAIChatStream(ctx context.Context, dst io.Writer, src io.Reader, stripExtraFields bool) (int64, error) {
	return copyOpenAIChatStreamWithOpts(ctx, dst, src, stripExtraFields, nil)
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
