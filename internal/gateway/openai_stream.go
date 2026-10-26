package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// StreamSSEAction describes how the gateway treated one OpenAI chat stream chunk.
type StreamSSEAction string

const (
	StreamSSEForward      StreamSSEAction = "forward"
	StreamSSEDrop         StreamSSEAction = "drop"
	StreamSSERewriteUsage StreamSSEAction = "rewrite_usage"
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

// sanitizeOpenAIStreamChunk enforces the public OpenAI stream contract on one JSON chunk.
// stripExtraFields removes Bifrost-only extra_fields before emitting to clients.
// On malformed JSON, fail open: emit the original bytes unchanged.
func sanitizeOpenAIStreamChunk(body []byte, st *openAIStreamSanitizer, stripExtraFields bool) (out []byte, action StreamSSEAction, emit bool) {
	if st == nil {
		st = newOpenAIStreamSanitizer()
	}
	action = StreamSSEForward
	changed := false

	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		if st.hasAnyFinished() {
			return nil, StreamSSEDrop, false
		}
		return body, action, true
	}
	if stripExtraFields {
		if _, has := root["extra_fields"]; has {
			delete(root, "extra_fields")
			changed = true
		}
	}

	rawChoices, hasChoices := root["choices"]
	if !hasChoices {
		if !changed {
			return body, action, true
		}
		out, err := json.Marshal(root)
		if err != nil {
			return body, action, true
		}
		return out, action, true
	}
	var choices []map[string]json.RawMessage
	if err := json.Unmarshal(rawChoices, &choices); err != nil {
		if st.hasAnyFinished() {
			return nil, StreamSSEDrop, false
		}
		return body, action, true
	}

	hasUsage := !isJSONNull(root["usage"])

	for _, ch := range choices {
		idx := streamChoiceIndex(ch)
		if !st.finished[idx] {
			continue
		}
		if hasUsage {
			root["choices"] = json.RawMessage("[]")
			action = StreamSSERewriteUsage
			changed = true
			break
		}
		return nil, StreamSSEDrop, false
	}

	for _, ch := range choices {
		idx := streamChoiceIndex(ch)
		if isTerminalStreamFinishReason(ch["finish_reason"]) {
			st.finished[idx] = true
		}
	}

	if fixed, ok := ensureStreamChoiceFinishReasonFromRoot(root); ok {
		root = fixed
		changed = true
	}

	if !changed {
		return body, action, true
	}
	out, err := json.Marshal(root)
	if err != nil {
		return body, action, true
	}
	return out, action, true
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

func openAIStreamJSONEqual(a, b []byte) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		return false
	}
	ba, err := json.Marshal(va)
	if err != nil {
		return false
	}
	bb, err := json.Marshal(vb)
	if err != nil {
		return false
	}
	return bytes.Equal(ba, bb)
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

func ensureStreamChoiceFinishReasonFromRoot(root map[string]json.RawMessage) (map[string]json.RawMessage, bool) {
	rawChoices, ok := root["choices"]
	if !ok {
		return root, false
	}
	var choices []map[string]json.RawMessage
	if err := json.Unmarshal(rawChoices, &choices); err != nil || len(choices) == 0 {
		return root, false
	}
	changed := false
	for i := range choices {
		if _, has := choices[i]["finish_reason"]; has {
			continue
		}
		choices[i]["finish_reason"] = json.RawMessage(jsonNull)
		changed = true
	}
	if !changed {
		return root, false
	}
	updatedChoices, err := json.Marshal(choices)
	if err != nil {
		return root, false
	}
	root["choices"] = updatedChoices
	return root, true
}

func recordProxyStreamChunk(ctx context.Context, openaiJSON []byte, action StreamSSEAction) {
	if action != StreamSSEDrop {
		return
	}
	idx := nextStreamChunkIndex(ctx)
	dumpIllegalStreamChunk(ctx, idx, openaiJSON, nil)
}

// copyOpenAIChatStream applies sanitizeOpenAIStreamChunk to each SSE data frame from src.
func copyOpenAIChatStream(ctx context.Context, dst io.Writer, src io.Reader, stripExtraFields bool) (int64, error) {
	c := &sseCopier{ctx: ctx, dst: dst, san: newOpenAIStreamSanitizer(), strip: stripExtraFields}
	br := bufio.NewReader(src)
	var event bytes.Buffer
	for {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return c.written, err
			}
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
	ctx      context.Context
	dst      io.Writer
	san      *openAIStreamSanitizer
	strip    bool
	written  int64
	seenDone bool
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
	sanitized, action, emit := sanitizeOpenAIStreamChunk(original, c.san, c.strip)
	if !emit || action == StreamSSEDrop {
		recordProxyStreamChunk(c.ctx, original, action)
		return
	}
	if !c.strip && action == StreamSSEForward && openAIStreamJSONEqual(original, sanitized) {
		sanitized = original
	}
	out.WriteString("data: ")
	out.Write(sanitized)
	out.WriteByte('\n')
}

// finish runs at EOF: it flushes a final event that had no blank line after it, or
// closes a finished stream that never sent [DONE].
func (c *sseCopier) finish(event *bytes.Buffer) error {
	if event.Len() > 0 {
		return c.flush(event)
	}
	if !c.seenDone && c.san.hasAnyFinished() {
		return c.write("data: [DONE]\n\n")
	}
	return nil
}

func (c *sseCopier) write(s string) error {
	n, err := io.WriteString(c.dst, s)
	c.written += int64(n)
	return err
}
