package observability

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestBifrostOTelTracer_sameTraceAsParent(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})

	parentCtx, parent := Tracer().Start(context.Background(), "polypus.chat")
	parentTrace := parent.SpanContext().TraceID()

	tr := NewBifrostOTelTracer()
	var iface schemas.Tracer = tr

	bctx := schemas.NewBifrostContext(parentCtx, schemas.NoDeadline)
	_, handle := iface.StartSpanID(bctx, "chat_completion_stream test-model", schemas.SpanKindLLMCall)
	bridge, ok := handle.(*otelSpanBridge)
	if !ok || bridge == nil {
		t.Fatal("expected otel span bridge handle")
	}
	bridge.schema.SetAttribute(schemas.AttrBifrostProviderName, string(schemas.Gemini))
	iface.EndSpan(handle, schemas.SpanStatusOk, "")
	if n := handleCount(tr); n != 0 {
		t.Fatalf("EndSpan through schemas.Tracer left %d handles", n)
	}
	parent.End()

	var childTrace string
	for _, sp := range exporter.GetSpans() {
		if sp.Name == "chat_completion_stream test-model" {
			childTrace = sp.SpanContext.TraceID().String()
			for _, a := range sp.Attributes {
				if a.Key == attrURLFull {
					if !strings.Contains(a.Value.AsString(), "generativelanguage.googleapis.com") {
						t.Fatalf("url.full missing host: %s", a.Value.AsString())
					}
					if !strings.Contains(a.Value.AsString(), "key=REDACTED") {
						t.Fatalf("url.full not redacted: %s", a.Value.AsString())
					}
				}
			}
		}
	}
	if childTrace == "" {
		t.Fatal("missing bifrost child span export")
	}
	if childTrace != parentTrace.String() {
		t.Fatalf("child trace %q != parent %q", childTrace, parentTrace)
	}
}

func TestBifrostOTelTracer_gateSendPassthrough(t *testing.T) {
	tr := NewBifrostOTelTracer()
	ch := make(chan *schemas.BifrostStreamChunk, 1)
	chunk := &schemas.BifrostStreamChunk{}
	bctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	ok := tr.GateSend("trace-1", chunk, false, false, ch, bctx)
	if !ok {
		t.Fatal("GateSend should forward via embedded NoOpTracer")
	}
	if len(ch) != 1 {
		t.Fatal("expected chunk on channel")
	}
}

func TestBifrostOTelTracer_concurrentRequestsKeepTheirOwnTrace(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})
	tr := NewBifrostOTelTracer()
	var iface schemas.Tracer = tr
	ctxA, spanA := Tracer().Start(context.Background(), "polypus.chat A")
	ctxB, spanB := Tracer().Start(context.Background(), "polypus.chat B")
	// Interleave like two concurrent requests on the one shared tracer.
	_, hA := iface.StartSpanID(schemas.NewBifrostContext(ctxA, schemas.NoDeadline), "llm A", schemas.SpanKindLLMCall)
	_, hB := iface.StartSpanID(schemas.NewBifrostContext(ctxB, schemas.NoDeadline), "llm B", schemas.SpanKindLLMCall)
	iface.EndSpan(hA, schemas.SpanStatusOk, "")
	iface.EndSpan(hB, schemas.SpanStatusOk, "")
	spanA.End()
	spanB.End()
	if n := handleCount(tr); n != 0 {
		t.Fatalf("EndSpan through schemas.Tracer left %d handles", n)
	}
	want := map[string]string{
		"llm A": spanA.SpanContext().TraceID().String(),
		"llm B": spanB.SpanContext().TraceID().String(),
	}
	exported := 0
	for _, sp := range exporter.GetSpans() {
		w, ok := want[sp.Name]
		if !ok {
			continue
		}
		exported++
		if sp.SpanContext.TraceID().String() != w {
			t.Fatalf("%s joined trace %s, want its own request's %s", sp.Name, sp.SpanContext.TraceID(), w)
		}
	}
	if exported != len(want) {
		t.Fatalf("exported %d bifrost spans, want %d", exported, len(want))
	}
	if got := tr.CreateTrace(""); len(got) != 32 {
		t.Fatalf("CreateTrace with no parent must return a fresh 32-hex id, got %q", got)
	}
}

func TestBifrostOTelTracer_EndSpanThroughInterfaceExportsAndReleases(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})
	tr := NewBifrostOTelTracer()
	var iface schemas.Tracer = tr
	bctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	_, handle := iface.StartSpan(bctx, "llm", schemas.SpanKindLLMCall)
	if handle == nil {
		t.Fatal("StartSpan returned a nil handle")
	}
	if n := handleCount(tr); n != 1 {
		t.Fatalf("after StartSpan: %d handles, want 1", n)
	}
	iface.EndSpan(handle, schemas.SpanStatusError, "boom")
	if n := handleCount(tr); n != 0 {
		t.Fatalf("after EndSpan: %d handles, want 0", n)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "llm" {
		t.Fatalf("exported spans = %+v, want one \"llm\" span", spans)
	}
	if spans[0].Status.Description != "boom" {
		t.Fatalf("status = %+v, want error \"boom\"", spans[0].Status)
	}
}

func TestBifrostOTelTracer_StartSpanIDNilContextNoSpan(t *testing.T) {
	tr := NewBifrostOTelTracer()
	//nolint:staticcheck // SA1012: a nil ctx is the case under test.
	id, handle := tr.StartSpanID(nil, "llm", schemas.SpanKindLLMCall)
	if id != "" || handle != nil {
		t.Fatalf("nil ctx: got (%q, %v), want no span", id, handle)
	}
	if n := handleCount(tr); n != 0 {
		t.Fatalf("nil ctx stored %d handles", n)
	}
}

func TestBifrostOTelTracer_llmTokenUsageAndCostSkip(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})

	parentCtx, parent := Tracer().Start(context.Background(), "polypus.chat")
	tr := NewBifrostOTelTracer()
	var iface schemas.Tracer = tr
	bctx := schemas.NewBifrostContext(parentCtx, schemas.NoDeadline)

	_, handle := iface.StartSpanID(bctx, "chat_completion test-model", schemas.SpanKindLLMCall)
	req := &schemas.BifrostRequest{
		ChatRequest: &schemas.BifrostChatRequest{
			Provider: schemas.OpenAI,
			Model:    "gpt-4o",
		},
	}
	iface.PopulateLLMRequestAttributes(handle, req)

	resp := &schemas.BifrostResponse{
		ChatResponse: &schemas.BifrostChatResponse{
			Model: "gpt-4o-2024",
			Usage: &schemas.BifrostLLMUsage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
				PromptTokensDetails: &schemas.ChatPromptTokensDetails{
					CachedReadTokens:  2,
					CachedWriteTokens: 3,
					AudioTokens:       7,
				},
				CompletionTokensDetails: &schemas.ChatCompletionTokensDetails{
					ReasoningTokens: 4,
				},
				Cost: &schemas.BifrostCost{TotalCost: 0.42},
			},
		},
	}
	resp.PopulateExtraFields(schemas.ChatCompletionRequest, schemas.OpenAI, "gpt-4o", "gpt-4o-2024")
	iface.PopulateLLMResponseAttributes(bctx, handle, resp, nil)
	iface.EndSpan(handle, schemas.SpanStatusOk, "")
	parent.End()

	child := findExportedSpan(exporter, "chat_completion test-model")
	if child == nil {
		t.Fatal("missing bifrost child span")
	}
	assertAttrInt64(t, child, attrLLMTokenCountPrompt, 10)
	assertAttrInt64(t, child, "gen_ai.usage.input_tokens", 10)
	assertAttrInt64(t, child, "gen_ai.usage.output_tokens", 5)
	assertAttrInt64(t, child, "gen_ai.usage.total_tokens", 15)
	assertAttrInt64(t, child, schemas.AttrUsageCacheReadInputTokens, 2)
	assertAttrInt64(t, child, schemas.AttrUsageReasoningOutputTokens, 4)
	assertAttrInt64(t, child, schemas.AttrPromptTokenDetailsAudio, 7)
	assertNoAttr(t, child, schemas.AttrUsageCost)
	assertNoAttrPrefix(t, child, "bifrost.cost.")
	assertAttrString(t, child, attrLLMProvider, "openai")
	assertNoAttr(t, child, schemas.AttrInputMessages)

	parentSpan := findExportedSpan(exporter, "polypus.chat")
	if parentSpan == nil {
		t.Fatal("missing parent span")
	}
	assertNoAttr(t, parentSpan, attrLLMTokenCountPrompt)
}

func TestBifrostOTelTracer_requestIdentityNoInputMessages(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})

	tr := NewBifrostOTelTracer()
	var iface schemas.Tracer = tr
	bctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	_, handle := iface.StartSpanID(bctx, "chat_completion gemini", schemas.SpanKindLLMCall)
	req := &schemas.BifrostRequest{
		ChatRequest: &schemas.BifrostChatRequest{
			Provider: schemas.Gemini,
			Model:    "gemini-2.0-flash",
		},
	}
	iface.PopulateLLMRequestAttributes(handle, req)
	iface.EndSpan(handle, schemas.SpanStatusOk, "")

	child := findExportedSpan(exporter, "chat_completion gemini")
	if child == nil {
		t.Fatal("missing child span")
	}
	assertAttrString(t, child, attrLLMProvider, "gcp.gemini")
	assertAttrString(t, child, attrLLMModelName, "gemini-2.0-flash")
	assertNoAttr(t, child, schemas.AttrInputMessages)
}

func TestBifrostOTelTracer_responseIdentityWithoutRequestHook(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})

	tr := NewBifrostOTelTracer()
	var iface schemas.Tracer = tr
	bctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	_, handle := iface.StartSpanID(bctx, "chat_completion openai", schemas.SpanKindLLMCall)
	resp := &schemas.BifrostResponse{
		ChatResponse: &schemas.BifrostChatResponse{
			Model: "gpt-4o-2024",
			Usage: &schemas.BifrostLLMUsage{PromptTokens: 3, CompletionTokens: 1, TotalTokens: 4},
		},
	}
	resp.PopulateExtraFields(schemas.ChatCompletionRequest, schemas.OpenAI, "gpt-4o", "gpt-4o-2024")
	iface.PopulateLLMResponseAttributes(bctx, handle, resp, nil)
	iface.EndSpan(handle, schemas.SpanStatusOk, "")

	child := findExportedSpan(exporter, "chat_completion openai")
	if child == nil {
		t.Fatal("missing child span")
	}
	assertAttrString(t, child, attrLLMProvider, "openai")
	assertAttrString(t, child, attrLLMModelName, "gpt-4o-2024")
	assertAttrString(t, child, schemas.AttrBifrostProviderName, string(schemas.OpenAI))
	assertAttrString(t, child, schemas.AttrOperationName, "chat")
	assertAttrInt64(t, child, attrLLMTokenCountPrompt, 3)
}

func TestBifrostOTelTracer_errorPathIdentityAndBilledUsage(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})

	tr := NewBifrostOTelTracer()
	var iface schemas.Tracer = tr
	bctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	bctx.SetValue(schemas.BifrostContextKeyRequestedProvider, schemas.Gemini)
	bctx.SetValue(schemas.BifrostContextKeyRequestedModel, "gemini-2.0-flash")
	_, handle := iface.StartSpanID(bctx, "chat_completion gemini-error", schemas.SpanKindLLMCall)
	berr := &schemas.BifrostError{
		Error: &schemas.ErrorField{Message: "upstream failed"},
		ExtraFields: schemas.BifrostErrorExtraFields{
			BilledUsage: &schemas.BifrostLLMUsage{PromptTokens: 6, TotalTokens: 6},
		},
	}
	berr.PopulateExtraFields(schemas.ChatCompletionRequest, schemas.Gemini, "gemini-2.0-flash", "")
	iface.PopulateLLMResponseAttributes(bctx, handle, nil, berr)
	iface.EndSpan(handle, schemas.SpanStatusError, "upstream failed")

	child := findExportedSpan(exporter, "chat_completion gemini-error")
	if child == nil {
		t.Fatal("missing child span")
	}
	assertAttrString(t, child, attrLLMProvider, "gcp.gemini")
	assertAttrString(t, child, attrLLMModelName, "gemini-2.0-flash")
	assertAttrString(t, child, schemas.AttrOperationName, "chat")
	assertAttrInt64(t, child, attrLLMTokenCountPrompt, 6)
}

func TestBifrostOTelTracer_streamDeferredSpanUsage(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})

	tr := NewBifrostOTelTracer()
	var iface schemas.Tracer = tr
	bctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	const traceID = "trace-stream-usage"

	_, handle := iface.StartSpanID(bctx, "chat_completion_stream test", schemas.SpanKindLLMCall)
	tr.StoreDeferredSpan(traceID, handle)
	tr.CreateStreamAccumulator(traceID, time.Now())
	tr.AddStreamingChunk(traceID, &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{Model: "m"}})
	chunk := &schemas.BifrostResponse{
		ChatResponse: &schemas.BifrostChatResponse{
			Model: "m",
			Usage: &schemas.BifrostLLMUsage{PromptTokens: 8, CompletionTokens: 2, TotalTokens: 10},
		},
	}
	chunk.PopulateExtraFields(schemas.ChatCompletionStreamRequest, schemas.OpenAI, "m", "m")
	tr.AddStreamingChunk(traceID, chunk)
	acc, _, n := tr.GetAccumulatedChunks(traceID)
	if acc == nil || n != 2 {
		t.Fatalf("accumulator: resp=%v chunks=%d", acc, n)
	}
	iface.PopulateLLMResponseAttributes(bctx, handle, acc, nil)
	iface.EndSpan(handle, schemas.SpanStatusOk, "")
	tr.CleanupStreamAccumulator(traceID)
	tr.ClearDeferredSpan(traceID)
	if tr.GetDeferredSpanHandle(traceID) != nil {
		t.Fatal("deferred handle still present after clear")
	}

	child := findExportedSpan(exporter, "chat_completion_stream test")
	if child == nil {
		t.Fatal("missing stream child span")
	}
	assertAttrInt64(t, child, attrLLMTokenCountPrompt, 8)
}

func findExportedSpan(exporter *tracetest.InMemoryExporter, name string) *tracetest.SpanStub {
	for i := range exporter.GetSpans() {
		if exporter.GetSpans()[i].Name == name {
			return &exporter.GetSpans()[i]
		}
	}
	return nil
}

func assertAttrInt64(t *testing.T, sp *tracetest.SpanStub, key string, want int64) {
	t.Helper()
	for _, a := range sp.Attributes {
		if string(a.Key) == key {
			if a.Value.Type() != attribute.INT64 {
				t.Fatalf("%s type %v, want int64", key, a.Value.Type())
			}
			if a.Value.AsInt64() != want {
				t.Fatalf("%s = %d, want %d", key, a.Value.AsInt64(), want)
			}
			return
		}
	}
	t.Fatalf("missing attribute %s on span %q", key, sp.Name)
}

func assertAttrString(t *testing.T, sp *tracetest.SpanStub, key, want string) {
	t.Helper()
	for _, a := range sp.Attributes {
		if string(a.Key) == key {
			if a.Value.AsString() != want {
				t.Fatalf("%s = %q, want %q", key, a.Value.AsString(), want)
			}
			return
		}
	}
	t.Fatalf("missing attribute %s on span %q", key, sp.Name)
}

func assertNoAttr(t *testing.T, sp *tracetest.SpanStub, key string) {
	t.Helper()
	for _, a := range sp.Attributes {
		if string(a.Key) == key {
			t.Fatalf("unexpected attribute %s = %v", key, a.Value.AsInterface())
		}
	}
}

func assertNoAttrPrefix(t *testing.T, sp *tracetest.SpanStub, prefix string) {
	t.Helper()
	for _, a := range sp.Attributes {
		if strings.HasPrefix(string(a.Key), prefix) {
			t.Fatalf("unexpected attribute %s", a.Key)
		}
	}
}

func handleCount(tr *BifrostOTelTracer) int {
	n := 0
	tr.handles.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}
