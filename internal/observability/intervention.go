package observability

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const attrIntervention = "polypus.intervention"

// Intervention labels a Polypus-owned rewrite or refuse on the wire.
type Intervention string

const (
	InterventionReasoningAlias            Intervention = "reasoning_alias"
	InterventionMergeReasoningIntoContent Intervention = "merge_reasoning_into_content"
	InterventionSSEDrop                   Intervention = "sse_drop"
	InterventionEmbedCapabilityMissing    Intervention = "embed_capability_missing"
)

// JoinInterventions merges codes in first-seen order without duplicates.
func JoinInterventions(existing string, code Intervention) string {
	code = Intervention(strings.TrimSpace(string(code)))
	if code == "" {
		return strings.TrimSpace(existing)
	}
	existing = strings.TrimSpace(existing)
	if existing == "" {
		return string(code)
	}
	for _, part := range strings.Split(existing, ",") {
		if strings.TrimSpace(part) == string(code) {
			return existing
		}
	}
	return existing + "," + string(code)
}

type interventionAccKey struct{}

type interventionAcc struct {
	joined string
}

// InterventionJoined returns comma-joined codes recorded on ctx, if any.
func InterventionJoined(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	acc, ok := ctx.Value(interventionAccKey{}).(*interventionAcc)
	if !ok || acc == nil {
		return ""
	}
	return acc.joined
}

// WithInterventionState attaches an accumulator for comma-joined intervention codes.
func WithInterventionState(ctx context.Context) context.Context {
	if ctx == nil {
		return ctx
	}
	if _, ok := ctx.Value(interventionAccKey{}).(*interventionAcc); ok {
		return ctx
	}
	return context.WithValue(ctx, interventionAccKey{}, &interventionAcc{})
}

// RecordIntervention annotates the active span when Polypus touched the response.
func RecordIntervention(ctx context.Context, code Intervention) {
	code = Intervention(strings.TrimSpace(string(code)))
	if code == "" || ctx == nil {
		return
	}
	joined := string(code)
	if acc, ok := ctx.Value(interventionAccKey{}).(*interventionAcc); ok && acc != nil {
		acc.joined = JoinInterventions(acc.joined, code)
		joined = acc.joined
	}
	span := trace.SpanFromContext(ctx)
	if span == nil || !span.IsRecording() {
		return
	}
	span.SetAttributes(attribute.String(attrIntervention, joined))
	span.AddEvent("polypus.intervention", trace.WithAttributes(
		attribute.String("polypus.intervention.code", string(code)),
	))
}
