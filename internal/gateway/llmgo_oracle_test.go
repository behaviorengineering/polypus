// Oracle tests use official github.com/XiaoConstantine/llm-go v0.3.5 only (strict OpenAI SSE).
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	llm "github.com/XiaoConstantine/llm-go"
	"github.com/XiaoConstantine/llm-go/openai"
	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/maximhq/bifrost/core/schemas"
)

func TestLLMGoModuleIsOfficialV035(t *testing.T) {
	// Guard against replace with the tolerant xynova fork.
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	body := string(mod)
	if strings.Contains(body, "replace github.com/XiaoConstantine/llm-go") {
		t.Fatal("go.mod must not replace llm-go in oracle tests")
	}
	if !strings.Contains(body, "github.com/XiaoConstantine/llm-go v0.3.5") {
		t.Fatalf("go.mod must pin llm-go v0.3.5, got:\n%s", body)
	}
}

type staticStreamRoundTrip struct {
	status      int
	contentType string
	body        string
}

func (response staticStreamRoundTrip) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: response.status,
		Status:     http.StatusText(response.status),
		Header:     http.Header{"Content-Type": {response.contentType}},
		Body:       io.NopCloser(strings.NewReader(response.body)),
	}, nil
}

// parseSSEWithLLMGo drains an SSE body through llm-go v0.3.5 Stream/Recv.
func parseSSEWithLLMGo(t *testing.T, body string, contentType string, caps ...llm.Capability) (text string, finish llm.FinishReason, terminal error) {
	t.Helper()
	if contentType == "" {
		contentType = "text/event-stream"
	}
	if len(caps) == 0 {
		caps = []llm.Capability{llm.CapabilityStreaming}
	}
	client, err := openai.New(openai.Config{
		Model:        "model",
		Capabilities: caps,
		BaseURL:      "http://example.com/v1",
		HTTPClient: &http.Client{Transport: staticStreamRoundTrip{
			status:      http.StatusOK,
			contentType: contentType,
			body:        body,
		}},
	})
	if err != nil {
		t.Fatalf("openai.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.Stream(ctx, llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.Part{{Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var b strings.Builder
	for {
		chunk, err := stream.Recv()
		if err != nil {
			_ = stream.Close()
			return b.String(), finish, err
		}
		for _, part := range chunk.Content {
			b.WriteString(part.Text)
		}
		if chunk.FinishReason != "" {
			finish = chunk.FinishReason
		}
	}
}

func proxySanitizeSSE(upstream string) string {
	var buf bytes.Buffer
	_, _ = copyOpenAIChatStream(context.Background(), &buf, strings.NewReader(upstream), false)
	return buf.String()
}

func proxySanitizeSSEStripExtra(upstream string) string {
	var buf bytes.Buffer
	_, _ = copyOpenAIChatStream(context.Background(), &buf, strings.NewReader(upstream), true)
	return buf.String()
}

func requireOracleEOF(t *testing.T, terminal error) {
	t.Helper()
	if !errors.Is(terminal, io.EOF) {
		t.Fatalf("terminal = %v, want io.EOF", terminal)
	}
}

func requireOracleMalformed(t *testing.T, terminal error, substr string) {
	t.Helper()
	var modelErr *llm.Error
	if !errors.As(terminal, &modelErr) || modelErr.Kind != llm.KindMalformedResponse {
		t.Fatalf("terminal = %v, want KindMalformedResponse", terminal)
	}
	if substr != "" && !strings.Contains(terminal.Error(), substr) {
		t.Fatalf("error = %q, want substring %q", terminal, substr)
	}
}

func legalHelloStream() string {
	return strings.Join([]string{
		"data: {\"id\":\"chat\",\"model\":\"model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n",
		"\n",
		"data: {\"id\":\"chat\",\"model\":\"model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n",
		"\n",
		"data: {\"id\":\"chat\",\"model\":\"model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n",
		"\n",
		"data: {\"id\":\"chat\",\"model\":\"model\",\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n",
		"\n",
		"data: [DONE]\n",
		"\n",
	}, "")
}

func validFinishJSON() string {
	return `{"id":"chat","model":"model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
}

func gemmaStreamWithTrailingChoice(content string) string {
	parts := []string{
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hi\"},\"finish_reason\":null}]}\n",
		"\n",
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n",
		"\n",
	}
	if content != "" {
		parts = append(parts,
			"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\""+content+"\"}}]}\n",
			"\n",
		)
	} else {
		parts = append(parts,
			"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":null}]}\n",
			"\n",
		)
	}
	parts = append(parts, "data: [DONE]\n", "\n")
	return strings.Join(parts, "")
}

func TestLLMGoOracleBaselineLegalAndTrailingChoice(t *testing.T) {
	text, finish, terminal := parseSSEWithLLMGo(t, legalHelloStream(), "")
	requireOracleEOF(t, terminal)
	if text != "hello" || finish != llm.FinishReasonStop {
		t.Fatalf("got text=%q finish=%q", text, finish)
	}

	raw := gemmaStreamWithTrailingChoice("")
	_, _, rawTerm := parseSSEWithLLMGo(t, raw, "")
	requireOracleMalformed(t, rawTerm, "after the finish reason")

	san := proxySanitizeSSE(raw)
	text, finish, terminal = parseSSEWithLLMGo(t, san, "")
	requireOracleEOF(t, terminal)
	if text != "Hi" || finish != llm.FinishReasonStop {
		t.Fatalf("sanitized got text=%q finish=%q body=%q", text, finish, san)
	}
}

func TestLLMGoOracleDualTable(t *testing.T) {
	vf := validFinishJSON()
	usageComplete := `{"id":"chat","model":"model","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`

	tests := []struct {
		name       string
		upstream   string
		stripExtra bool
		rawSub     string // malformed substring; empty => expect EOF.
		rawText    string
		sanSub     string
		sanText    string
		sanFinish  llm.FinishReason
	}{
		{
			name:      "empty trailing choice after stop",
			upstream:  gemmaStreamWithTrailingChoice(""),
			rawSub:    "after the finish reason",
			sanText:   "Hi",
			sanFinish: llm.FinishReasonStop,
		},
		{
			name:      "non-empty trailing text",
			upstream:  gemmaStreamWithTrailingChoice("late"),
			rawSub:    "after the finish reason",
			sanText:   "Hi",
			sanFinish: llm.FinishReasonStop,
		},
		{
			name: "second finish-only delta",
			upstream: strings.Join([]string{
				"data: " + vf + "\n", "\n",
				"data: {\"id\":\"chat\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n", "\n",
				"data: [DONE]\n", "\n",
			}, ""),
			rawSub:    "after the finish reason",
			sanFinish: llm.FinishReasonStop,
		},
		{
			name: "trailing tool-call delta after stop",
			upstream: strings.Join([]string{
				"data: " + vf + "\n", "\n",
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c\",\"type\":\"function\",\"function\":{\"name\":\"f\",\"arguments\":\"{}\"}}]},\"finish_reason\":null}]}\n", "\n",
				"data: [DONE]\n", "\n",
			}, ""),
			rawSub:    "after the finish reason",
			sanFinish: llm.FinishReasonStop,
		},
		{
			name: "usage with choice trailer",
			upstream: strings.Join([]string{
				"data: " + vf + "\n", "\n",
				"data: {\"id\":\"chat\",\"choices\":[{\"index\":0,\"delta\":{}}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n", "\n",
				"data: [DONE]\n", "\n",
			}, ""),
			rawSub:    "after the finish reason",
			sanFinish: llm.FinishReasonStop,
		},
		{
			name: "usage-only trailer",
			upstream: strings.Join([]string{
				"data: " + vf + "\n", "\n",
				"data: " + usageComplete + "\n", "\n",
				"data: [DONE]\n", "\n",
			}, ""),
			sanFinish: llm.FinishReasonStop,
		},
		{
			name: "missing finish_reason mid-stream",
			upstream: strings.Join([]string{
				"data: {\"id\":\"chat\",\"model\":\"model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\n", "\n",
				"data: " + vf + "\n", "\n",
				"data: " + usageComplete + "\n", "\n",
				"data: [DONE]\n", "\n",
			}, ""),
			rawSub:    "no finish reason",
			sanText:   "x",
			sanFinish: llm.FinishReasonStop,
		},
		{
			name: "malformed JSON before finish",
			upstream: strings.Join([]string{
				"data: not-json\n", "\n",
				"data: [DONE]\n", "\n",
			}, ""),
			rawSub: "decode event",
			sanSub: "decode event",
		},
		{
			name: "malformed JSON after stop",
			upstream: strings.Join([]string{
				"data: " + vf + "\n", "\n",
				"data: not-json\n", "\n",
				"data: [DONE]\n", "\n",
			}, ""),
			rawSub:    "decode event",
			sanFinish: llm.FinishReasonStop,
		},
		{
			name: "truncated tail at EOF",
			upstream: strings.Join([]string{
				"data: " + vf + "\n", "\n",
				"data: {\n",
			}, ""),
			rawSub: "without [DONE]",
			sanSub: "without [DONE]",
		},
		{
			name: "missing DONE after stop",
			upstream: strings.Join([]string{
				"data: {\"id\":\"chat\",\"model\":\"model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"z\"},\"finish_reason\":null}]}\n", "\n",
				"data: " + vf + "\n", "\n",
				"data: " + usageComplete + "\n", "\n",
			}, ""),
			rawSub:    "without [DONE]",
			sanText:   "z",
			sanFinish: llm.FinishReasonStop,
		},
		{
			name:     "DONE before finish",
			upstream: "data: [DONE]\n\n",
			rawSub:   "before a finish reason",
			sanSub:   "before a finish reason",
		},
		{
			name: "incomplete usage",
			upstream: strings.Join([]string{
				"data: " + vf + "\n", "\n",
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1}}\n", "\n",
				"data: [DONE]\n", "\n",
			}, ""),
			rawSub: "incomplete token usage",
			sanSub: "incomplete token usage",
		},
		{
			name: "repeated usage",
			upstream: strings.Join([]string{
				"data: " + vf + "\n", "\n",
				"data: " + usageComplete + "\n", "\n",
				"data: " + usageComplete + "\n", "\n",
				"data: [DONE]\n", "\n",
			}, ""),
			rawSub: "repeated usage",
			sanSub: "repeated usage",
		},
		{
			name:      "CRLF trailing choice",
			upstream:  strings.ReplaceAll(gemmaStreamWithTrailingChoice(""), "\n", "\r\n"),
			rawSub:    "after the finish reason",
			sanText:   "Hi",
			sanFinish: llm.FinishReasonStop,
		},
		{
			name:      "BOM prefix",
			upstream:  "\ufeff" + gemmaStreamWithTrailingChoice(""),
			rawSub:    "after the finish reason",
			sanText:   "Hi",
			sanFinish: llm.FinishReasonStop,
		},
		{
			name:       "extra_fields strip bifrost",
			upstream:   strings.Replace(gemmaStreamWithTrailingChoice(""), "\"id\":\"c1\"", "\"id\":\"c1\",\"extra_fields\":{\"x\":1}", 1),
			stripExtra: true,
			rawSub:     "after the finish reason",
			sanText:    "Hi",
			sanFinish:  llm.FinishReasonStop,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, rawTerm := parseSSEWithLLMGo(t, tc.upstream, "")
			if tc.rawSub == "" {
				requireOracleEOF(t, rawTerm)
			} else {
				requireOracleMalformed(t, rawTerm, tc.rawSub)
			}
			if tc.rawText != "" {
				text, _, _ := parseSSEWithLLMGo(t, tc.upstream, "")
				if text != tc.rawText {
					t.Fatalf("raw text = %q want %q", text, tc.rawText)
				}
			}

			var san string
			if tc.stripExtra {
				san = proxySanitizeSSEStripExtra(tc.upstream)
			} else {
				san = proxySanitizeSSE(tc.upstream)
			}
			text, finish, sanTerm := parseSSEWithLLMGo(t, san, "")
			if tc.sanSub == "" {
				requireOracleEOF(t, sanTerm)
			} else {
				requireOracleMalformed(t, sanTerm, tc.sanSub)
			}
			if tc.sanText != "" && text != tc.sanText {
				t.Fatalf("sanitized text = %q want %q\nbody=%q", text, tc.sanText, san)
			}
			if tc.sanFinish != "" && finish != tc.sanFinish {
				t.Fatalf("sanitized finish = %q want %q", finish, tc.sanFinish)
			}
		})
	}
}

func TestLLMGoOraclePerIndexSanitizerForwardsLLMGoLeaveLoud(t *testing.T) {
	upstream := strings.Join([]string{
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n", "\n",
		"data: {\"choices\":[{\"index\":1,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n", "\n",
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n", "\n",
		"data: [DONE]\n", "\n",
	}, "")
	san := newOpenAIStreamSanitizer()
	sanitizeOpenAIStreamChunk([]byte(`{"choices":[{"index":0,"finish_reason":"stop","delta":{}}]}`), san, false)
	out, _, emit := sanitizeOpenAIStreamChunk([]byte(`{"choices":[{"index":1,"delta":{"content":"ok"}}]}`), san, false)
	if !emit || !strings.Contains(string(out), `"content":"ok"`) {
		t.Fatalf("sanitizer should forward index 1 after index 0 stop")
	}
	_, _, rawTerm := parseSSEWithLLMGo(t, upstream, "")
	requireOracleMalformed(t, rawTerm, "after the finish reason")
	_, _, sanTerm := parseSSEWithLLMGo(t, proxySanitizeSSE(upstream), "")
	requireOracleMalformed(t, sanTerm, "after the finish reason")
}

type oneByteReader struct {
	s string
	i int
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if r.i >= len(r.s) {
		return 0, io.EOF
	}
	p[0] = r.s[r.i]
	r.i++
	return 1, nil
}

func TestLLMGoOracleOneByteReaderTrailingChoice(t *testing.T) {
	raw := gemmaStreamWithTrailingChoice("")
	var buf bytes.Buffer
	_, err := copyOpenAIChatStream(context.Background(), &buf, &oneByteReader{s: raw}, false)
	if err != nil {
		t.Fatal(err)
	}
	text, finish, terminal := parseSSEWithLLMGo(t, buf.String(), "")
	requireOracleEOF(t, terminal)
	if text != "Hi" || finish != llm.FinishReasonStop {
		t.Fatalf("got %q %q", text, finish)
	}
}

func TestLLMGoOracleWrongContentType(t *testing.T) {
	_, _, terminal := parseSSEWithLLMGo(t, "{}", "application/json")
	requireOracleMalformed(t, terminal, "content type")
}

func TestCopyOpenAIChatStreamRespectsCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := strings.Repeat("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"},\"finish_reason\":null}]}\n\n", 1000)
	var buf bytes.Buffer
	_, err := copyOpenAIChatStream(ctx, &buf, strings.NewReader(src), false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v want canceled", err)
	}
}

func TestProxyStreamDropWritesFailureDump(t *testing.T) {
	dir := t.TempDir()

	upstream := gemmaStreamWithTrailingChoice("late")
	var buf bytes.Buffer
	_, err := copyOpenAIChatStream(withStreamChunkCounter(context.Background(), testShared.log, dir), &buf, strings.NewReader(upstream), false)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 dump file, got %d", len(entries))
	}
}

const dualReasoningOracleSubstr = "both reasoning_content and reasoning"

// TestLLMGoOracleDualReasoningAliasFromBifrostMarshal records the Muse Spark
// composition failure: Polypus json.Marshal of a Bifrost chat object emits
// OpenRouter's reasoning_content alias next to reasoning (same string, two
// keys). The gateway forwards both. llm-go v0.3.5 treats that as malformed
// and never surfaces content.
func TestLLMGoOracleDualReasoningAliasFromBifrostMarshal(t *testing.T) {
	reasoning := "hidden"
	content := "visible"
	streamJSON, err := json.Marshal(museSparkStreamChunk(reasoning, content))
	if err != nil {
		t.Fatalf("marshal stream: %v", err)
	}
	if !choiceHasBothReasoningSpellings(streamJSON) {
		t.Fatalf("Bifrost stream marshal did not emit both spellings: %s", streamJSON)
	}

	chunks := make(chan router.ChatStreamChunk, 2)
	errCh := make(chan error, 1)
	chunks <- router.ChatStreamChunk{OpenAIJSON: streamJSON}
	stopJSON, err := json.Marshal(museSparkStreamStop())
	if err != nil {
		t.Fatalf("marshal stop: %v", err)
	}
	chunks <- router.ChatStreamChunk{OpenAIJSON: stopJSON}
	close(chunks)
	close(errCh)
	rec := httptest.NewRecorder()
	ctx := observability.WithInterventionState(context.Background())
	if err := writeBifrostChatSSE(ctx, rec, chunks, errCh); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := rec.Header().Get(config.InterventionHeader); got != "reasoning_alias" {
		t.Fatalf("intervention header=%q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"reasoning"`) || !strings.Contains(body, `"reasoning_content"`) {
		t.Fatalf("gateway stripped a reasoning spelling:\n%s", body)
	}

	text, _, terminal := parseSSEWithLLMGo(t, body, "")
	requireOracleMalformed(t, terminal, dualReasoningOracleSubstr)
	if text != "" {
		t.Fatalf("llm-go must not surface content after dual-reasoning reject, got %q", text)
	}

	completionJSON, err := json.Marshal(museSparkCompletion(reasoning, content))
	if err != nil {
		t.Fatalf("marshal completion: %v", err)
	}
	fixed, changed := mergeReasoningIntoContent(completionJSON)
	if changed {
		t.Fatalf("content was already set; merge must not rewrite: %s", fixed)
	}
	if !choiceHasBothReasoningSpellings(completionJSON) {
		t.Fatalf("non-stream marshal did not emit both spellings: %s", completionJSON)
	}
	genText, genErr := parseJSONWithLLMGo(t, string(completionJSON))
	requireOracleMalformed(t, genErr, dualReasoningOracleSubstr)
	if genText != "" {
		t.Fatalf("Generate must not surface content after dual-reasoning reject, got %q", genText)
	}
}

func TestLLMGoOracleDualReasoningProxyPassthrough(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"id":"chat","model":"model","choices":[{"index":0,"delta":{"reasoning":"hidden","reasoning_content":"hidden","content":"visible"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chat","model":"model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	san := proxySanitizeSSE(upstream)
	if !strings.Contains(san, `"reasoning"`) || !strings.Contains(san, `"reasoning_content"`) {
		t.Fatalf("proxy sanitizer stripped a reasoning spelling:\n%s", san)
	}
	rec := httptest.NewRecorder()
	ctx := observability.WithInterventionState(context.Background())
	_, err := copyOpenAIChatStreamWithOpts(ctx, rec, strings.NewReader(san), false, &streamCopyOpts{
		responseWriter:   rec,
		statusCode:       http.StatusOK,
		delayWriteHeader: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.Header().Get(config.InterventionHeader); got != "reasoning_alias" {
		t.Fatalf("proxy intervention header=%q", got)
	}
	text, _, terminal := parseSSEWithLLMGo(t, san, "")
	requireOracleMalformed(t, terminal, dualReasoningOracleSubstr)
	if text != "" {
		t.Fatalf("llm-go must not surface content after dual-reasoning reject, got %q", text)
	}
}

// TestLLMGoOracleGemmaThinkingMergeFillsContent records the Gemma thinking
// rewrite: empty content plus one reasoning_content spelling. Merge copies
// the answer into content and llm-go can parse. Bifrost marshal of the same
// thought still emits both spellings, so merge does not fix the alias reject.
func TestLLMGoOracleGemmaThinkingMergeFillsContent(t *testing.T) {
	answer := "visible"
	t.Run("one_spelling_merge_parses", func(t *testing.T) {
		raw := gemmaThinkingCompletionJSON(t, answer)
		if choiceHasBothReasoningSpellings(raw) {
			t.Fatalf("Gemma fixture must not emit both spellings: %s", raw)
		}
		fixed, changed := mergeReasoningIntoContent(raw)
		if !changed {
			t.Fatalf("empty content must merge from reasoning_content: %s", raw)
		}
		if choiceHasBothReasoningSpellings(fixed) {
			t.Fatalf("merge must not add a second spelling: %s", fixed)
		}
		got, err := parseJSONWithLLMGo(t, string(fixed))
		if err != nil {
			t.Fatalf("llm-go after Gemma merge: %v body=%s", err, fixed)
		}
		if got != answer {
			t.Fatalf("content=%q want %q", got, answer)
		}
	})
	t.Run("bifrost_marshal_still_dual_key_reject", func(t *testing.T) {
		raw, err := json.Marshal(gemmaBifrostEmptyContent(answer))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !choiceHasBothReasoningSpellings(raw) {
			t.Fatalf("Bifrost marshal must emit both spellings: %s", raw)
		}
		fixed, changed := mergeReasoningIntoContent(raw)
		if !changed {
			t.Fatalf("empty content must still merge: %s", raw)
		}
		if !choiceHasBothReasoningSpellings(fixed) {
			t.Fatalf("merge must leave both spellings: %s", fixed)
		}
		got, genErr := parseJSONWithLLMGo(t, string(fixed))
		requireOracleMalformed(t, genErr, dualReasoningOracleSubstr)
		if got != "" {
			t.Fatalf("llm-go must not surface merged content after dual-key reject, got %q", got)
		}
	})
}

func gemmaThinkingCompletionJSON(t *testing.T, answer string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"id":     "chat",
		"object": "chat.completion",
		"model":  "cf_local/@cf/google/gemma-4-26b-a4b-it",
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role":              "assistant",
				"content":           "",
				"reasoning_content": answer,
			},
			"finish_reason": "stop",
		}},
	})
	if err != nil {
		t.Fatalf("marshal Gemma fixture: %v", err)
	}
	return raw
}

func gemmaBifrostEmptyContent(answer string) *schemas.BifrostChatResponse {
	empty := ""
	stop := "stop"
	return &schemas.BifrostChatResponse{
		ID:     "chat",
		Object: "chat.completion",
		Model:  "cf_local/@cf/google/gemma-4-26b-a4b-it",
		Choices: []schemas.BifrostResponseChoice{{
			Index:        0,
			FinishReason: &stop,
			ChatNonStreamResponseChoice: &schemas.ChatNonStreamResponseChoice{
				Message: &schemas.ChatMessage{
					Role:    schemas.ChatMessageRoleAssistant,
					Content: &schemas.ChatMessageContent{ContentStr: &empty},
					ChatAssistantMessage: &schemas.ChatAssistantMessage{
						Reasoning: &answer,
					},
				},
			},
		}},
	}
}

func museSparkStreamChunk(reasoning, content string) *schemas.BifrostChatResponse {
	return &schemas.BifrostChatResponse{
		ID:     "chat",
		Object: "chat.completion.chunk",
		Model:  "openrouter/meta/muse-spark-1.3",
		Choices: []schemas.BifrostResponseChoice{{
			Index: 0,
			ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
				Delta: &schemas.ChatStreamResponseChoiceDelta{
					Reasoning: &reasoning,
					Content:   &content,
				},
			},
		}},
	}
}

func museSparkStreamStop() *schemas.BifrostChatResponse {
	stop := "stop"
	return &schemas.BifrostChatResponse{
		ID:     "chat",
		Object: "chat.completion.chunk",
		Model:  "openrouter/meta/muse-spark-1.3",
		Choices: []schemas.BifrostResponseChoice{{
			Index:        0,
			FinishReason: &stop,
			ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
				Delta: &schemas.ChatStreamResponseChoiceDelta{},
			},
		}},
	}
}

func museSparkCompletion(reasoning, content string) *schemas.BifrostChatResponse {
	stop := "stop"
	return &schemas.BifrostChatResponse{
		ID:     "chat",
		Object: "chat.completion",
		Model:  "openrouter/meta/muse-spark-1.3",
		Choices: []schemas.BifrostResponseChoice{{
			Index:        0,
			FinishReason: &stop,
			ChatNonStreamResponseChoice: &schemas.ChatNonStreamResponseChoice{
				Message: &schemas.ChatMessage{
					Role:    schemas.ChatMessageRoleAssistant,
					Content: &schemas.ChatMessageContent{ContentStr: &content},
					ChatAssistantMessage: &schemas.ChatAssistantMessage{
						Reasoning: &reasoning,
					},
				},
			},
		}},
	}
}

func parseJSONWithLLMGo(t *testing.T, body string) (text string, terminal error) {
	t.Helper()
	client, err := openai.New(openai.Config{
		Model:   "model",
		BaseURL: "http://example.com/v1",
		HTTPClient: &http.Client{Transport: staticStreamRoundTrip{
			status:      http.StatusOK,
			contentType: "application/json",
			body:        body,
		}},
	})
	if err != nil {
		t.Fatalf("openai.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := client.Generate(ctx, llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.Part{{Text: "hi"}}}},
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, part := range resp.Message.Content {
		b.WriteString(part.Text)
	}
	return b.String(), nil
}
