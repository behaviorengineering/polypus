package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/behaviorengineering/polypus/internal/observability"
)

// streamSSEAction describes how the gateway treated one OpenAI chat stream chunk.
type streamSSEAction string

const (
	streamSSEForward      streamSSEAction = "forward"
	streamSSEDrop         streamSSEAction = "drop"
	streamSSERewriteUsage streamSSEAction = "rewrite_usage"
)

// openAIStreamSanitizer tracks per-choice-index terminal finish_reason for one stream.
type openAIStreamSanitizer struct {
	finished map[int]bool
}

func newOpenAIStreamSanitizer() *openAIStreamSanitizer {
	return &openAIStreamSanitizer{finished: make(map[int]bool)}
}

func (st *openAIStreamSanitizer) hasAnyFinished() bool {
	if st == nil {
		return false
	}
	for _, done := range st.finished {
		if done {
			return true
		}
	}
	return false
}

// sanitizedChunk is what the sanitizer made of one stream chunk.
type sanitizedChunk struct {
	// out is the chunk to emit; nil when action is streamSSEDrop.
	out    []byte
	action streamSSEAction
	// alias is set when the chunk carried both reasoning spellings.
	alias bool
}

// sanitizeOpenAIStreamChunk enforces the public OpenAI stream contract on one JSON chunk.
// stripExtraFields removes Bifrost-only extra_fields before emitting to clients.
// On malformed JSON, fail open: emit the original bytes unchanged.
func sanitizeOpenAIStreamChunk(body []byte, st *openAIStreamSanitizer, stripExtraFields bool) sanitizedChunk {
	if st == nil {
		st = newOpenAIStreamSanitizer()
	}
	chunk, ok := decodeChatChunk(body)
	if !ok {
		if st.hasAnyFinished() {
			return sanitizedChunk{action: streamSSEDrop}
		}
		return sanitizedChunk{out: body, action: streamSSEForward}
	}
	changed := stripExtraFields && chunk.deleteRootKey(keyExtraFields)
	action := streamSSEForward
	late := st.afterFinish(chunk.choices)
	st.markFinished(chunk.choices)
	if late {
		if !chunk.hasUsage() {
			return sanitizedChunk{action: streamSSEDrop}
		}
		// The usage tail is legal after finish_reason; late choices are not.
		chunk.choices = []map[string]json.RawMessage{}
		action = streamSSERewriteUsage
		changed = true
	}
	changed = ensureChoiceFinishReasons(chunk.choices) || changed
	alias := stripDualReasoningAlias(chunk.choices)
	return sanitizedChunk{out: chunk.encodeIfChanged(body, changed || alias), action: action, alias: alias}
}

// afterFinish reports whether any of choices continues an index that already sent
// its terminal finish_reason.
func (st *openAIStreamSanitizer) afterFinish(choices []map[string]json.RawMessage) bool {
	for _, ch := range choices {
		if st.finished[streamChoiceIndex(ch)] {
			return true
		}
	}
	return false
}

// markFinished records each choice index that sends a terminal finish_reason.
func (st *openAIStreamSanitizer) markFinished(choices []map[string]json.RawMessage) {
	for _, ch := range choices {
		if isTerminalStreamFinishReason(ch[keyFinishReason]) {
			st.finished[streamChoiceIndex(ch)] = true
		}
	}
}

// chatChunk is one OpenAI chat object, a stream chunk or a whole completion,
// decoded once so each contract rule edits the same maps.
type chatChunk struct {
	root map[string]json.RawMessage
	// choices is the decoded choices array; nil when root has none.
	choices []map[string]json.RawMessage
}

// decodeChatChunk decodes raw. ok is false when raw is not a JSON object or its
// choices are not an array of objects.
func decodeChatChunk(raw []byte) (chatChunk, bool) {
	var c chatChunk
	if err := json.Unmarshal(raw, &c.root); err != nil {
		return chatChunk{}, false
	}
	rawChoices, ok := c.root[keyChoices]
	if !ok {
		return c, true
	}
	if err := json.Unmarshal(rawChoices, &c.choices); err != nil {
		return chatChunk{}, false
	}
	return c, true
}

func (c *chatChunk) deleteRootKey(key string) bool {
	if _, ok := c.root[key]; !ok {
		return false
	}
	delete(c.root, key)
	return true
}

func (c *chatChunk) hasUsage() bool {
	return !isJSONNull(c.root["usage"])
}

// encode marshals the chunk with its edited choices.
func (c *chatChunk) encode() ([]byte, error) {
	if c.choices != nil {
		raw, err := json.Marshal(c.choices)
		if err != nil {
			return nil, err
		}
		c.root[keyChoices] = raw
	}
	return json.Marshal(c.root)
}

// encodeIfChanged returns the re-encoded chunk when changed, else original. A chunk
// that cannot be encoded fails open to original.
func (c *chatChunk) encodeIfChanged(original []byte, changed bool) []byte {
	if !changed {
		return original
	}
	out, err := c.encode()
	if err != nil {
		return original
	}
	return out
}

func streamChoiceIndex(ch map[string]json.RawMessage) int {
	raw := ch["index"]
	if len(raw) == 0 {
		return 0
	}
	var idx int
	if err := json.Unmarshal(raw, &idx); err != nil {
		return 0
	}
	return idx
}

func isTerminalStreamFinishReason(raw json.RawMessage) bool {
	if isJSONNull(raw) {
		return false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return false
	}
	return strings.TrimSpace(s) != ""
}

// ensureChoiceFinishReasons gives every choice a finish_reason key (null mid-stream),
// which OpenAI-compatible clients such as llm-go require.
func ensureChoiceFinishReasons(choices []map[string]json.RawMessage) bool {
	changed := false
	for _, ch := range choices {
		if _, has := ch[keyFinishReason]; has {
			continue
		}
		ch[keyFinishReason] = json.RawMessage(jsonNull)
		changed = true
	}
	return changed
}

func jsonFieldPresent(obj map[string]json.RawMessage, key string) bool {
	raw, ok := obj[key]
	if !ok {
		return false
	}
	return !isJSONNull(raw)
}

// choiceObjectFields are the choice members that carry a message: delta in a stream
// chunk, message in a completion.
var choiceObjectFields = []string{"delta", "message"}

// editChoiceObject decodes ch[field] as an object, runs edit on it and stores it back
// when edit reports a change.
func editChoiceObject(ch map[string]json.RawMessage, field string, edit func(obj map[string]json.RawMessage) bool) bool {
	raw, ok := ch[field]
	if !ok {
		return false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return false
	}
	if !edit(obj) {
		return false
	}
	updated, err := json.Marshal(obj)
	if err != nil {
		return false
	}
	ch[field] = updated
	return true
}

// stripDualReasoningAlias keeps OpenAI "reasoning" and deletes the OpenRouter alias
// wherever a delta or message carries both. It reports whether it found any.
func stripDualReasoningAlias(choices []map[string]json.RawMessage) bool {
	changed := false
	for _, ch := range choices {
		for _, field := range choiceObjectFields {
			changed = editChoiceObject(ch, field, func(obj map[string]json.RawMessage) bool {
				if !jsonFieldPresent(obj, keyReasoning) || !jsonFieldPresent(obj, keyReasoningContent) {
					return false
				}
				delete(obj, keyReasoningContent)
				return true
			}) || changed
		}
	}
	return changed
}

// mergeReasoningIntoContent copies a message's reasoning into its content when the
// content is empty, so clients that read only content get the answer.
func mergeReasoningIntoContent(choices []map[string]json.RawMessage) bool {
	changed := false
	for _, ch := range choices {
		changed = editChoiceObject(ch, "message", func(msg map[string]json.RawMessage) bool {
			if jsonStringField(msg["content"]) != "" {
				return false
			}
			reasoning := firstNonEmpty(jsonStringField(msg[keyReasoningContent]), jsonStringField(msg[keyReasoning]))
			if reasoning == "" {
				return false
			}
			raw, err := json.Marshal(reasoning)
			if err != nil {
				return false
			}
			msg["content"] = raw
			return true
		}) || changed
	}
	return changed
}

// completionFixes says which contract fixes normalizeCompletionBody applied.
type completionFixes struct {
	merged bool // reasoning was copied into empty content.
	alias  bool // a duplicate reasoning_content alias was dropped.
}

// normalizeCompletionBody applies the OpenAI contract fixes to a non-stream chat
// completion. A body it cannot decode or encode passes through unchanged.
func normalizeCompletionBody(raw []byte) ([]byte, completionFixes) {
	chunk, ok := decodeChatChunk(raw)
	if !ok || len(chunk.choices) == 0 {
		return raw, completionFixes{}
	}
	fixes := completionFixes{merged: mergeReasoningIntoContent(chunk.choices)}
	fixes.alias = stripDualReasoningAlias(chunk.choices)
	if !fixes.merged && !fixes.alias {
		return raw, fixes
	}
	out, err := chunk.encode()
	if err != nil {
		return raw, completionFixes{}
	}
	return out, fixes
}

// normalizeChatCompletion applies the OpenAI contract fixes to a non-stream body and
// records each intervention on the response and the span.
func normalizeChatCompletion(ctx context.Context, w http.ResponseWriter, raw []byte) []byte {
	out, fixes := normalizeCompletionBody(raw)
	if fixes.merged {
		setIntervention(ctx, w, observability.InterventionMergeReasoningIntoContent)
	}
	if fixes.alias {
		setIntervention(ctx, w, observability.InterventionReasoningAlias)
	}
	return out
}
