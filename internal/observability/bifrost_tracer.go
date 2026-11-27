package observability

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	attrURLFull             = "url.full"
	geminiSyntheticSpanHint = "polypus.bifrost.gemini"
)

// BifrostOTelTracer maps Bifrost schemas.Tracer hooks to OpenTelemetry spans. One
// tracer serves every request, so it holds no per-request state: the parent span
// comes from the context Bifrost passes in, and the router puts each request's
// trace ID on its BifrostContext. Stream gate methods stay on the embedded NoOpTracer.
type BifrostOTelTracer struct {
	schemas.NoOpTracer

	handles     sync.Map // span handle -> *otelSpanBridge.
	deferred    sync.Map // trace ID -> schemas.SpanHandle.
	streamAccum sync.Map // trace ID -> *streamChunkAccumulator.
}

type streamChunkAccumulator struct {
	mu     sync.Mutex
	chunks []*schemas.BifrostResponse
}

var _ schemas.Tracer = (*BifrostOTelTracer)(nil)

// NewBifrostOTelTracer returns a tracer suitable for schemas.BifrostConfig.Tracer.
func NewBifrostOTelTracer() *BifrostOTelTracer {
	return &BifrostOTelTracer{}
}

// CreateTrace returns parentID, or a new random trace ID. Bifrost calls it only when
// the request context carries no BifrostContextKeyTraceID; the router sets that key
// from the request's span, so traced requests never reach here.
func (t *BifrostOTelTracer) CreateTrace(parentID string, _ ...string) string {
	if parentID != "" {
		return parentID
	}
	return strings.ReplaceAll(uuid.New().String(), "-", "")
}

// StartSpan creates an OTel child span and returns an updated Go context.
func (t *BifrostOTelTracer) StartSpan(ctx context.Context, name string, kind schemas.SpanKind) (context.Context, schemas.SpanHandle) {
	_, handle := t.StartSpanID(ctx, name, kind)
	if handle == nil {
		return ctx, nil
	}
	if bridge, ok := handle.(*otelSpanBridge); ok && bridge.goCtx != nil {
		return bridge.goCtx, handle
	}
	return ctx, handle
}

// StartSpanID mirrors Bifrost LLM spans into OTel children of the inbound context.
// A nil ctx yields no span ("", nil) rather than an orphan root span.
func (t *BifrostOTelTracer) StartSpanID(ctx context.Context, name string, kind schemas.SpanKind) (string, schemas.SpanHandle) {
	if ctx == nil {
		return "", nil
	}
	oiKind := openInferenceKind(kind)
	newCtx, otelSpan := Tracer().Start(ctx, name, trace.WithSpanKind(otelSpanKind(kind)))
	otelSpan.SetAttributes(attribute.String("openinference.span.kind", oiKind))
	if kind == schemas.SpanKindLLMCall {
		otelSpan.SetAttributes(attribute.String("polypus.bifrost.span", "llm_call"))
	}

	schemaSpan := &schemas.Span{
		SpanID:     otelSpan.SpanContext().SpanID().String(),
		TraceID:    otelSpan.SpanContext().TraceID().String(),
		Name:       name,
		Kind:       kind,
		Attributes: map[string]any{},
	}
	bridge := &otelSpanBridge{
		schema: schemaSpan,
		otel:   otelSpan,
		goCtx:  newCtx,
	}
	if t != nil {
		t.handles.Store(bridge, bridge)
	}
	return schemaSpan.SpanID, bridge
}

// EndSpan implements schemas.Tracer: it ends the OTel span and drops the handle.
func (t *BifrostOTelTracer) EndSpan(handle schemas.SpanHandle, status schemas.SpanStatus, statusMsg string) {
	bridge, ok := handle.(*otelSpanBridge)
	if !ok || bridge == nil || bridge.otel == nil {
		return
	}
	flushBifrostSpanAttributes(bridge)
	switch status {
	case schemas.SpanStatusError:
		bridge.otel.SetStatus(codes.Error, statusMsg)
	case schemas.SpanStatusOk:
		bridge.otel.SetStatus(codes.Ok, "")
	case schemas.SpanStatusUnset:
		// Bifrost did not decide (a cancelled stream, an early return); OTel treats
		// Ok as final, so the status stays unset rather than green.
	default:
		// An unknown status is no verdict either.
	}
	bridge.otel.End()
	if t != nil {
		t.handles.Delete(bridge)
	}
}

// PopulateLLMRequestAttributes implements schemas.Tracer.
func (t *BifrostOTelTracer) PopulateLLMRequestAttributes(handle schemas.SpanHandle, req *schemas.BifrostRequest) {
	bridge, ok := handle.(*otelSpanBridge)
	if !ok || bridge == nil || bridge.otel == nil || req == nil {
		return
	}
	populateLLMRequestUsage(bridge.schema, req)
	flushBifrostSpanAttributes(bridge)
}

// PopulateLLMResponseAttributes implements schemas.Tracer.
func (t *BifrostOTelTracer) PopulateLLMResponseAttributes(bctx *schemas.BifrostContext, handle schemas.SpanHandle, resp *schemas.BifrostResponse, err *schemas.BifrostError) {
	bridge, ok := handle.(*otelSpanBridge)
	if !ok || bridge == nil || bridge.otel == nil {
		return
	}
	if err != nil && err.Error != nil {
		bridge.schema.SetAttribute("error.message", err.Error.Message)
	}
	populateLLMResponseUsage(bridge.schema, resp, err, bctx)
	flushBifrostSpanAttributes(bridge)
}

// StoreDeferredSpan implements schemas.Tracer.
func (t *BifrostOTelTracer) StoreDeferredSpan(traceID string, handle schemas.SpanHandle) {
	if t == nil || traceID == "" || handle == nil {
		return
	}
	t.deferred.Store(traceID, handle)
}

// GetDeferredSpanHandle implements schemas.Tracer.
func (t *BifrostOTelTracer) GetDeferredSpanHandle(traceID string) schemas.SpanHandle {
	if t == nil || traceID == "" {
		return nil
	}
	v, ok := t.deferred.Load(traceID)
	if !ok {
		return nil
	}
	handle, ok := v.(schemas.SpanHandle)
	if !ok {
		return nil
	}
	return handle
}

// ClearDeferredSpan implements schemas.Tracer.
func (t *BifrostOTelTracer) ClearDeferredSpan(traceID string) {
	if t == nil || traceID == "" {
		return
	}
	t.deferred.Delete(traceID)
}

// GetDeferredSpanID implements schemas.Tracer.
func (t *BifrostOTelTracer) GetDeferredSpanID(traceID string) string {
	handle := t.GetDeferredSpanHandle(traceID)
	bridge, ok := handle.(*otelSpanBridge)
	if !ok || bridge == nil || bridge.schema == nil {
		return ""
	}
	return bridge.schema.SpanID
}

// CreateStreamAccumulator implements schemas.Tracer.
func (t *BifrostOTelTracer) CreateStreamAccumulator(traceID string, _ time.Time) {
	if t == nil || traceID == "" {
		return
	}
	t.streamAccum.Store(traceID, &streamChunkAccumulator{})
}

// AddStreamingChunk implements schemas.Tracer.
func (t *BifrostOTelTracer) AddStreamingChunk(traceID string, response *schemas.BifrostResponse) {
	if t == nil || traceID == "" || response == nil {
		return
	}
	v, ok := t.streamAccum.Load(traceID)
	if !ok {
		return
	}
	acc, ok := v.(*streamChunkAccumulator)
	if !ok || acc == nil {
		return
	}
	acc.mu.Lock()
	acc.chunks = append(acc.chunks, response)
	acc.mu.Unlock()
}

// GetAccumulatedChunks implements schemas.Tracer.
func (t *BifrostOTelTracer) GetAccumulatedChunks(traceID string) (*schemas.BifrostResponse, int64, int) {
	if t == nil || traceID == "" {
		return nil, 0, 0
	}
	v, ok := t.streamAccum.Load(traceID)
	if !ok {
		return nil, 0, 0
	}
	acc, ok := v.(*streamChunkAccumulator)
	if !ok || acc == nil {
		return nil, 0, 0
	}
	acc.mu.Lock()
	defer acc.mu.Unlock()
	if len(acc.chunks) == 0 {
		return nil, 0, 0
	}
	var last *schemas.BifrostResponse
	for i := len(acc.chunks) - 1; i >= 0; i-- {
		if acc.chunks[i] != nil {
			last = acc.chunks[i]
			if last.NormalizedUsage() != nil {
				break
			}
		}
	}
	if last == nil {
		last = acc.chunks[len(acc.chunks)-1]
	}
	return last, 0, len(acc.chunks)
}

// CleanupStreamAccumulator implements schemas.Tracer.
func (t *BifrostOTelTracer) CleanupStreamAccumulator(traceID string) {
	if t == nil || traceID == "" {
		return
	}
	t.streamAccum.Delete(traceID)
}

// ResetStreamAccumulator implements schemas.Tracer.
func (t *BifrostOTelTracer) ResetStreamAccumulator(traceID string) {
	if t == nil || traceID == "" {
		return
	}
	v, ok := t.streamAccum.Load(traceID)
	if !ok {
		return
	}
	acc, ok := v.(*streamChunkAccumulator)
	if !ok || acc == nil {
		return
	}
	acc.mu.Lock()
	acc.chunks = nil
	acc.mu.Unlock()
}

// flushBifrostSpanAttributes copies the Bifrost span's attributes onto the OTel span.
// Bifrost writes them through SpanFromHandle under the span's own (unexported) lock,
// and late writers (streaming finalisation, redaction) may still be writing, so the
// map is read only through Bifrost's locked export snapshot, never ranged directly.
func flushBifrostSpanAttributes(bridge *otelSpanBridge) {
	if bridge == nil || bridge.otel == nil || bridge.schema == nil {
		return
	}
	snap := (&schemas.Trace{Spans: []*schemas.Span{bridge.schema}}).SnapshotForExport()
	defer snap.ReleaseSnapshot()
	if len(snap.Spans) == 0 || snap.Spans[0] == nil {
		return
	}
	attrs := snap.Spans[0].Attributes
	for k, v := range attrs {
		if v == nil {
			continue
		}
		bridge.otel.SetAttributes(bifrostAttrToOTel(k, v))
	}
	if prov, ok := attrs[schemas.AttrBifrostProviderName]; ok && strings.EqualFold(fmt.Sprint(prov), string(schemas.Gemini)) {
		bridge.otel.SetAttributes(
			attribute.String(attrURLFull, geminiSyntheticURLFull()),
			attribute.String("polypus.bifrost.synthetic_provider", geminiSyntheticSpanHint),
		)
	}
}

// SetAttribute implements schemas.Tracer.
func (t *BifrostOTelTracer) SetAttribute(handle schemas.SpanHandle, key string, value any) {
	span := t.SpanFromHandle(handle)
	if span != nil {
		span.SetAttribute(key, value)
	}
}

// SpanFromHandle implements schemas.Tracer.
func (t *BifrostOTelTracer) SpanFromHandle(handle schemas.SpanHandle) *schemas.Span {
	bridge, ok := handle.(*otelSpanBridge)
	if !ok || bridge == nil || bridge.schema == nil {
		return nil
	}
	return bridge.schema
}

// GetSpanHandleByID implements schemas.Tracer.
func (t *BifrostOTelTracer) GetSpanHandleByID(_ string, _ *string) schemas.SpanHandle {
	return nil
}

// AddEvent implements schemas.Tracer.
func (t *BifrostOTelTracer) AddEvent(handle schemas.SpanHandle, name string, attrs map[string]any) {
	bridge, ok := handle.(*otelSpanBridge)
	if !ok || bridge == nil || bridge.otel == nil {
		return
	}
	otelAttrs := make([]attribute.KeyValue, 0, len(attrs))
	for k, v := range attrs {
		otelAttrs = append(otelAttrs, bifrostAttrToOTel(k, v))
	}
	bridge.otel.AddEvent(name, trace.WithAttributes(otelAttrs...))
}

// EndTrace implements schemas.Tracer.
func (t *BifrostOTelTracer) EndTrace(_ string) *schemas.Trace { return nil }

// Stop implements schemas.Tracer.
func (t *BifrostOTelTracer) Stop() {
	if t == nil {
		return
	}
	t.handles.Range(func(key, _ any) bool {
		if bridge, ok := key.(*otelSpanBridge); ok && bridge != nil && bridge.otel != nil {
			bridge.otel.End()
		}
		t.handles.Delete(key)
		return true
	})
	clearSyncMap(&t.deferred)
	clearSyncMap(&t.streamAccum)
}

func clearSyncMap(m *sync.Map) {
	if m == nil {
		return
	}
	m.Range(func(key, _ any) bool {
		m.Delete(key)
		return true
	})
}

type otelSpanBridge struct {
	schema *schemas.Span
	otel   trace.Span
	goCtx  context.Context
}

func bifrostAttrToOTel(key string, value any) attribute.KeyValue {
	switch v := value.(type) {
	case string:
		return attribute.String(key, redactURLsInText(v))
	case bool:
		return attribute.Bool(key, v)
	case int:
		return attribute.Int(key, v)
	case int64:
		return attribute.Int64(key, v)
	case float64:
		return attribute.Float64(key, v)
	case []string:
		return attribute.StringSlice(key, v)
	default:
		return attribute.String(key, redactURLsInText(fmt.Sprint(value)))
	}
}

// geminiSyntheticURLFull is the url.full a Gemini span reports: the endpoint shape
// with the key already redacted, since no real key reaches the tracer.
func geminiSyntheticURLFull() string {
	return "https://generativelanguage.googleapis.com/v1beta/models/gemini:streamGenerateContent?key=" + url.QueryEscape(redactedSecret)
}

func openInferenceKind(kind schemas.SpanKind) string {
	//exhaustive:ignore // only LLM calls are LLM spans; every other Bifrost kind is a CHAIN step
	switch kind {
	case schemas.SpanKindLLMCall:
		return "LLM"
	default:
		return "CHAIN"
	}
}

func otelSpanKind(kind schemas.SpanKind) trace.SpanKind {
	//exhaustive:ignore // only LLM calls leave the process; every other Bifrost kind is internal
	switch kind {
	case schemas.SpanKindLLMCall:
		return trace.SpanKindClient
	default:
		return trace.SpanKindInternal
	}
}
