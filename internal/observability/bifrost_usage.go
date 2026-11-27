package observability

import (
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

const (
	//nolint:gosec // OpenInference attribute keys, not secrets.
	attrLLMTokenCountPrompt = "llm.token_count.prompt"
	//nolint:gosec // OpenInference attribute keys, not secrets.
	attrLLMTokenCountCompletion = "llm.token_count.completion"
	//nolint:gosec // OpenInference attribute keys, not secrets.
	attrLLMTokenCountTotal = "llm.token_count.total"
	//nolint:gosec // OpenInference attribute keys, not secrets.
	attrLLMTokenCountPromptCacheRead = "llm.token_count.prompt_details.cache_read"
	//nolint:gosec // OpenInference attribute keys, not secrets.
	attrLLMTokenCountPromptCacheWrite = "llm.token_count.prompt_details.cache_write"
	//nolint:gosec // OpenInference attribute keys, not secrets.
	attrLLMTokenCountCompletionReasoning = "llm.token_count.completion_details.reasoning"
	attrLLMModelName                     = "llm.model_name"
	attrLLMProvider                      = "llm.provider"
)

func isBifrostCostAttributeKey(key string) bool {
	if key == schemas.AttrUsageCost {
		return true
	}
	return strings.HasPrefix(key, "bifrost.cost.")
}

func usageWithoutCost(u *schemas.BifrostLLMUsage) *schemas.BifrostLLMUsage {
	if u == nil {
		return nil
	}
	copy := u.DeepCopy()
	if copy == nil {
		return nil
	}
	copy.Cost = nil
	copy.CostInUsdTicks = nil
	copy.NormalizeReasoningTokens()
	return copy
}

func bifrostRequestType(req *schemas.BifrostRequest) schemas.RequestType {
	if req == nil {
		return ""
	}
	switch {
	case req.ChatRequest != nil:
		return schemas.ChatCompletionRequest
	case req.TextCompletionRequest != nil:
		return schemas.TextCompletionRequest
	case req.ResponsesRequest != nil:
		return schemas.ResponsesRequest
	case req.EmbeddingRequest != nil:
		return schemas.EmbeddingRequest
	case req.DecisionRequest != nil:
		return schemas.DecisionRequest
	default:
		return ""
	}
}

func applyLLMIdentityAttributes(span *schemas.Span, llm *schemas.LLMSpanData) {
	if span == nil || llm == nil {
		return
	}
	attrs := map[string]any{
		schemas.AttrProviderName:        schemas.OTelProviderName(llm.Provider),
		schemas.AttrBifrostProviderName: string(llm.Provider),
		schemas.AttrRequestModel:        llm.RequestModel,
	}
	if llm.RequestType != "" {
		attrs[schemas.AttrOperationName] = schemas.OTelOperationName(llm.RequestType)
	}
	if llm.Alias != "" {
		attrs[schemas.AttrBifrostAlias] = llm.Alias
	}
	for k, v := range attrs {
		if v == "" {
			continue
		}
		span.SetAttribute(k, v)
	}
}

func setBifrostRenderedAttributes(span *schemas.Span, rendered map[string]any) {
	if span == nil || len(rendered) == 0 {
		return
	}
	for k, v := range rendered {
		if v == nil || isBifrostCostAttributeKey(k) {
			continue
		}
		span.SetAttribute(k, v)
	}
}

func overlayOpenInferenceTokenCounts(span *schemas.Span, u *schemas.BifrostLLMUsage) {
	if span == nil || u == nil {
		return
	}
	span.SetAttribute(attrLLMTokenCountPrompt, u.PromptTokens)
	span.SetAttribute(attrLLMTokenCountCompletion, u.CompletionTokens)
	total := u.TotalTokens
	if total == 0 {
		total = u.PromptTokens + u.CompletionTokens
	}
	span.SetAttribute(attrLLMTokenCountTotal, total)
	if pd := u.PromptTokensDetails; pd != nil {
		if pd.CachedReadTokens > 0 {
			span.SetAttribute(attrLLMTokenCountPromptCacheRead, pd.CachedReadTokens)
		}
		if pd.CachedWriteTokens > 0 {
			span.SetAttribute(attrLLMTokenCountPromptCacheWrite, pd.CachedWriteTokens)
		}
	}
	if cd := u.CompletionTokensDetails; cd != nil && cd.ReasoningTokens > 0 {
		span.SetAttribute(attrLLMTokenCountCompletionReasoning, cd.ReasoningTokens)
	}
}

func overlayOpenInferenceBilledIdentity(span *schemas.Span, provider schemas.ModelProvider, requestModel, responseModel string) {
	if span == nil {
		return
	}
	model := responseModel
	if model == "" {
		model = requestModel
	}
	if model != "" {
		span.SetAttribute(attrLLMModelName, model)
	}
	if provider != "" {
		span.SetAttribute(attrLLMProvider, schemas.OTelProviderName(provider))
	}
}

func populateLLMRequestUsage(span *schemas.Span, req *schemas.BifrostRequest) {
	if span == nil || req == nil {
		return
	}
	provider, model, _ := req.GetRequestFields()
	llm := span.EnsureLLMIfMatch(span.SpanID)
	if llm == nil {
		return
	}
	llm.Cost = nil
	llm.Provider = provider
	llm.RequestType = bifrostRequestType(req)
	llm.RequestModel = model
	applyLLMIdentityAttributes(span, llm)
	overlayOpenInferenceBilledIdentity(span, provider, model, "")
}

func populateLLMResponseUsage(span *schemas.Span, resp *schemas.BifrostResponse, req *schemas.BifrostRequest) {
	if span == nil {
		return
	}
	llm := span.EnsureLLMIfMatch(span.SpanID)
	if llm == nil {
		return
	}
	if req != nil {
		provider, model, _ := req.GetRequestFields()
		if provider != "" {
			llm.Provider = provider
		}
		if model != "" {
			llm.RequestModel = model
		}
		if llm.RequestType == "" {
			llm.RequestType = bifrostRequestType(req)
		}
	}
	provider := llm.Provider
	responseModel := ""
	if resp != nil {
		if provider == "" {
			if ef := resp.GetExtraFields(); ef != nil && ef.RoutingInfo.Provider != "" {
				provider = ef.RoutingInfo.Provider
				llm.Provider = provider
			}
		}
		responseModel = resp.ServedModel()
		if responseModel != "" {
			llm.ResponseModel = responseModel
		}
		llm.Usage = usageWithoutCost(resp.NormalizedUsage())
		llm.Cost = nil
		setBifrostRenderedAttributes(span, llm.ResponseAttributes())
		if llm.Usage != nil {
			overlayOpenInferenceTokenCounts(span, llm.Usage)
		}
	}
	overlayOpenInferenceBilledIdentity(span, provider, llm.RequestModel, responseModel)
}
