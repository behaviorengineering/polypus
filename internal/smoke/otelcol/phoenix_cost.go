package otelcol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"go.opentelemetry.io/otel/attribute"
)

const (
	openaiCostSmokeModel       = "gpt-4o"
	openaiCostSmokeProvider    = "openai"
	customCostSmokeProvider    = "polypus-smoke"
	customCostSmokePrefix      = "cf_local/@cf/polypus-cost-smoke-"
	smokePromptTokens          = int64(10)
	smokeCompletionTokens      = int64(5)
	smokeTotalTokens           = int64(15)
	customPromptUSDPerMTok     = 1_000_000.0
	customCompletionUSDPerMTok = 2_000_000.0
	// phoenixModelStoreRefresh is one tick past Phoenix GenerativeModelStore's
	// default 5s CostModelLookup refresh. createModel writes the DB row at
	// once; ingest still prices from the previous cache, and that cost is
	// never recalculated.
	phoenixModelStoreRefresh = 6 * time.Second
)

// provePhoenixCost is the operator proof for Phoenix Settings → Models: seed a
// custom namePattern via GraphQL createModel, export one matching LLM span and
// one LiteLLM-priced leaf, then wait until each trace has costSummary > 0.
func provePhoenixCost(ctx context.Context, opts CollectorOptions) error {
	suffix, err := smokeModelSuffix()
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, opRunCollector, "phoenix cost model suffix")
	}
	customModel := customCostSmokePrefix + suffix
	if err := phoenixCreateModel(ctx, opts, phoenixCreateModelInput{
		Name:        "polypus-cost-smoke-" + suffix,
		Provider:    customCostSmokeProvider,
		NamePattern: "^" + regexp.QuoteMeta(customModel) + "$",
		Costs: []phoenixTokenPriceInput{
			{TokenType: "input", Kind: "PROMPT", CostPerMillionTokens: customPromptUSDPerMTok},
			{TokenType: "output", Kind: "COMPLETION", CostPerMillionTokens: customCompletionUSDPerMTok},
		},
	}); err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, opRunCollector, "phoenix createModel")
	}
	if err := sleepContext(ctx, phoenixModelStoreRefresh); err != nil {
		return derrors.Wrap(err, contextCodeOr(ctx, derrors.CodeTimeout), opRunCollector, "context ended before phoenix cost was confirmed")
	}

	openaiTrace, err := exportLLMCostSpan(ctx, opts.OTLPEndpoint, "polypus-local-collector-smoke-cost-openai", openaiCostSmokeModel, openaiCostSmokeProvider)
	if err != nil {
		return derrors.Wrap(err, contextCodeOr(ctx, derrors.CodeUnavailable), opRunCollector, "otlp openai cost export")
	}
	customTrace, err := exportLLMCostSpan(ctx, opts.OTLPEndpoint, "polypus-local-collector-smoke-cost-custom", customModel, customCostSmokeProvider)
	if err != nil {
		return derrors.Wrap(err, contextCodeOr(ctx, derrors.CodeUnavailable), opRunCollector, "otlp custom cost export")
	}
	opts.Logger.Info("collector smoke: phoenix cost export ok",
		"custom_trace_id", customTrace, "openai_trace_id", openaiTrace, "custom_model", customModel)

	if err := waitProbe(ctx, opts.PollInterval, func() error {
		next, err := probePhoenixCost(ctx, opts, customTrace, openaiTrace, customModel)
		if next != "" {
			customTrace = next
			opts.Logger.Info("collector smoke: re-exported custom cost span", "custom_trace_id", customTrace)
		}
		return err
	}, "phoenix cost not confirmed before the deadline", "context ended before phoenix cost was confirmed"); err != nil {
		return err
	}
	opts.Logger.Info("collector smoke: phoenix cost ok", "custom_trace_id", customTrace, "openai_trace_id", openaiTrace)
	return nil
}

// probePhoenixCost returns a replacement custom trace id when that leaf was
// ingested unpriced (Phoenix priced from a stale model cache).
func probePhoenixCost(ctx context.Context, opts CollectorOptions, customTrace, openaiTrace, customModel string) (string, error) {
	custom, err := phoenixTraceCostGraphQL(ctx, opts, customTrace)
	if err != nil {
		return "", fmt.Errorf("custom leaf: %w", err)
	}
	if customNeedsReexport(custom) {
		next, expErr := exportLLMCostSpan(ctx, opts.OTLPEndpoint, "polypus-local-collector-smoke-cost-custom",
			customModel, customCostSmokeProvider)
		if expErr != nil {
			return "", fmt.Errorf("custom leaf re-export: %w", expErr)
		}
		return next, fmt.Errorf("phoenix cost: custom leaf ingested before model store refresh")
	}
	var errs []error
	if err := costProbeError("custom leaf", custom); err != nil {
		errs = append(errs, err)
	}
	openai, err := phoenixTraceCostGraphQL(ctx, opts, openaiTrace)
	if err != nil {
		errs = append(errs, fmt.Errorf("openai leaf: %w", err))
	} else if err := costProbeError("openai leaf", openai); err != nil {
		errs = append(errs, err)
	}
	return "", errors.Join(errs...)
}

func customNeedsReexport(got phoenixTraceCost) bool {
	return got.traceFound && !got.priced && (got.promptTokens > 0 || got.completionTokens > 0)
}

func costProbeError(leaf string, got phoenixTraceCost) error {
	if !got.traceFound {
		return fmt.Errorf("phoenix cost: %s not ingested", leaf)
	}
	if got.priced {
		return nil
	}
	if got.promptTokens == 0 && got.completionTokens == 0 {
		return fmt.Errorf("phoenix cost: %s missing token counts", leaf)
	}
	return fmt.Errorf("phoenix cost: %s tokens present but cost is 0", leaf)
}

func exportLLMCostSpan(ctx context.Context, endpoint, spanName, model, provider string) (string, error) {
	traceID, err := exportCollectorSpan(ctx, endpoint, spanName, llmCostSpanAttrs(model, provider)...)
	if err != nil {
		return "", err
	}
	return normalizeTraceIDHex(traceID)
}

func llmCostSpanAttrs(model, provider string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("openinference.span.kind", "LLM"),
		attribute.String("llm.model_name", model),
		attribute.String("llm.provider", provider),
		attribute.Int64("llm.token_count.prompt", smokePromptTokens),
		attribute.Int64("llm.token_count.completion", smokeCompletionTokens),
		attribute.Int64("llm.token_count.total", smokeTotalTokens),
		attribute.String("smoke.probe", "phoenix_cost"),
	}
}

func smokeModelSuffix() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
