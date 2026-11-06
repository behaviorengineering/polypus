package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
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

func recordProxyStreamChunk(ctx context.Context, openaiJSON []byte, action streamSSEAction) {
	if action != streamSSEDrop {
		return
	}
	observability.RecordIntervention(ctx, observability.InterventionSSEDrop)
	idx := nextStreamChunkIndex(ctx)
	dumpIllegalStreamChunk(ctx, idx, openaiJSON, nil)
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

// streamCopyOpts configures delayed HTTP status for proxy SSE.
type streamCopyOpts struct {
	responseWriter   http.ResponseWriter
	statusCode       int
	delayWriteHeader bool
}

// copyOpenAIChatStreamWithOpts applies sanitizeOpenAIStreamChunk to each SSE data
// frame from src. opts is nil when dst is not the HTTP response.
func copyOpenAIChatStreamWithOpts(ctx context.Context, dst io.Writer, src io.Reader, stripExtraFields bool, opts *streamCopyOpts) (int64, error) {
	if ctx == nil {
		return 0, derrors.New(derrors.CodeInvalid, "gateway.copyOpenAIChatStream", "context required")
	}
	c := &sseCopier{ctx: ctx, dst: dst, san: newOpenAIStreamSanitizer(), strip: stripExtraFields}
	if opts != nil {
		c.httpWriter = opts.responseWriter
		c.statusCode = opts.statusCode
		c.delayWriteHeader = opts.delayWriteHeader
	}
	br := bufio.NewReader(src)
	var event bytes.Buffer
	for {
		if err := ctx.Err(); err != nil {
			return c.written, err
		}
		line, err := br.ReadString('\n')
		switch {
		case line == "\n" || line == "\r\n":
			// A blank line ends an SSE event.
			if ferr := c.flush(&event); ferr != nil {
				return c.written, ferr
			}
			continue
		case line != "":
			event.WriteString(line)
		}
		if errors.Is(err, io.EOF) {
			return c.written, c.finish(&event)
		}
		if err != nil {
			return c.written, err
		}
	}
}

// sseCopier rewrites one OpenAI SSE stream event by event.
type sseCopier struct {
	ctx              context.Context
	dst              io.Writer
	san              *openAIStreamSanitizer
	strip            bool
	written          int64
	seenDone         bool
	httpWriter       http.ResponseWriter
	statusCode       int
	delayWriteHeader bool
	headersStarted   bool
}

// flush rewrites the buffered event and writes it; an event that ends up empty is dropped.
func (c *sseCopier) flush(event *bytes.Buffer) error {
	if event.Len() == 0 {
		return nil
	}
	payload := event.String()
	event.Reset()
	var out strings.Builder
	for _, line := range strings.Split(payload, "\n") {
		c.rewriteLine(line, &out)
	}
	if out.Len() == 0 {
		return nil
	}
	out.WriteByte('\n')
	return c.write(out.String())
}

// rewriteLine copies one event line to out: non-data lines as they are, [DONE] as
// is, and data frames through the sanitizer (dropped frames are recorded, not sent).
func (c *sseCopier) rewriteLine(line string, out *strings.Builder) {
	if line == "" {
		return
	}
	if !strings.HasPrefix(line, "data:") {
		out.WriteString(line)
		out.WriteByte('\n')
		return
	}
	data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if data == "[DONE]" {
		c.seenDone = true
		out.WriteString("data: [DONE]\n")
		return
	}
	original := []byte(data)
	res := sanitizeOpenAIStreamChunk(original, c.san, c.strip)
	if res.action == streamSSEDrop {
		recordProxyStreamChunk(c.ctx, original, res.action)
		return
	}
	if res.alias {
		observability.RecordIntervention(c.ctx, observability.InterventionReasoningAlias)
	}
	out.WriteString("data: ")
	out.Write(res.out)
	out.WriteByte('\n')
}

// finish runs at EOF: it flushes a final event that had no blank line after it, or
// closes a finished stream that never sent [DONE].
func (c *sseCopier) finish(event *bytes.Buffer) error {
	if event.Len() > 0 {
		return c.flush(event)
	}
	if !c.seenDone && c.san.hasAnyFinished() {
		c.ensureResponseStarted()
		return c.write("data: [DONE]\n\n")
	}
	return nil
}

func (c *sseCopier) ensureResponseStarted() {
	if !c.delayWriteHeader || c.httpWriter == nil || c.headersStarted {
		return
	}
	if joined := observability.InterventionJoined(c.ctx); joined != "" {
		c.httpWriter.Header().Set(config.InterventionHeader, joined)
	}
	c.httpWriter.WriteHeader(c.statusCode)
	c.headersStarted = true
}

func (c *sseCopier) write(s string) error {
	if strings.TrimSpace(s) != "" {
		c.ensureResponseStarted()
	}
	n, err := io.WriteString(c.dst, s)
	c.written += int64(n)
	return err
}
