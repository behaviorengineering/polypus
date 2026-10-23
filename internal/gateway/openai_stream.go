package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	emit = true
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

	hasUsage := len(root["usage"]) > 0 && string(root["usage"]) != "null"

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
	if len(raw) == 0 || string(raw) == "null" {
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
		choices[i]["finish_reason"] = json.RawMessage("null")
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
	br := bufio.NewReader(src)
	san := newOpenAIStreamSanitizer()
	var written int64
	var eventBuf bytes.Buffer
	var seenDone bool

	flushEvent := func() error {
		if eventBuf.Len() == 0 {
			return nil
		}
		payload := eventBuf.Bytes()
		eventBuf.Reset()

		lines := strings.Split(string(payload), "\n")
		var outEvent strings.Builder
		for _, line := range lines {
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				outEvent.WriteString(line)
				outEvent.WriteByte('\n')
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				seenDone = true
				outEvent.WriteString("data: [DONE]\n")
				continue
			}
			original := []byte(data)
			out, action, emit := sanitizeOpenAIStreamChunk(original, san, stripExtraFields)
			if !emit || action == StreamSSEDrop {
				recordProxyStreamChunk(ctx, original, action)
				continue
			}
			if !stripExtraFields && action == StreamSSEForward && openAIStreamJSONEqual(original, out) {
				out = original
			}
			outEvent.WriteString("data: ")
			outEvent.Write(out)
			outEvent.WriteByte('\n')
		}
		if outEvent.Len() == 0 {
			return nil
		}
		outEvent.WriteByte('\n')
		n, err := dst.Write([]byte(outEvent.String()))
		written += int64(n)
		return err
	}

	for {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return written, err
			}
		}
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			if line == "\n" || line == "\r\n" {
				if err := flushEvent(); err != nil {
					return written, err
				}
				continue
			}
			eventBuf.WriteString(line)
		}
		if err != nil {
			if err == io.EOF {
				if eventBuf.Len() > 0 {
					if flushErr := flushEvent(); flushErr != nil {
						return written, flushErr
					}
					return written, nil
				}
				if !seenDone && san.hasAnyFinished() {
					n, werr := io.WriteString(dst, "data: [DONE]\n\n")
					written += int64(n)
					if werr != nil {
						return written, werr
					}
				}
				return written, nil
			}
			return written, err
		}
	}
}
