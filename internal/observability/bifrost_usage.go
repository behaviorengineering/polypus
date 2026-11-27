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
	cloned := u.DeepCopy()
	if cloned == nil {
		return nil
	}
	cloned.Cost = nil
	cloned.CostInUsdTicks = nil
	cloned.NormalizeReasoningTokens()
	return cloned
}

func bifrostRequestType(req *schemas.BifrostRequest) schemas.RequestType {
	if req == nil {
		return ""
	}
	if req.RequestType != "" {
		return req.RequestType
	}
	return inferRequestTypeFromPointers(req)
}

func inferRequestTypeFromPointers(req *schemas.BifrostRequest) schemas.RequestType {
	if req == nil {
		return ""
	}
	if rt := inferLLMFamilyRequestType(req); rt != "" {
		return rt
	}
	if rt := inferMediaFamilyRequestType(req); rt != "" {
		return rt
	}
	return inferAdminFamilyRequestType(req)
}

func inferLLMFamilyRequestType(req *schemas.BifrostRequest) schemas.RequestType {
	switch {
	case req.ListModelsRequest != nil:
		return schemas.ListModelsRequest
	case req.ModelRetrieveRequest != nil:
		return schemas.ModelRetrieveRequest
	case req.TextCompletionRequest != nil:
		return schemas.TextCompletionRequest
	case req.ChatRequest != nil:
		return schemas.ChatCompletionRequest
	case req.ResponsesRequest != nil:
		return schemas.ResponsesRequest
	case req.ResponsesRetrieveRequest != nil:
		return schemas.ResponsesRetrieveRequest
	case req.ResponsesDeleteRequest != nil:
		return schemas.ResponsesDeleteRequest
	case req.ResponsesCancelRequest != nil:
		return schemas.ResponsesCancelRequest
	case req.ResponsesInputItemsRequest != nil:
		return schemas.ResponsesInputItemsRequest
	case req.CountTokensRequest != nil:
		return schemas.CountTokensRequest
	case req.CompactionRequest != nil:
		return schemas.CompactionRequest
	case req.EmbeddingRequest != nil:
		return schemas.EmbeddingRequest
	case req.RerankRequest != nil:
		return schemas.RerankRequest
	case req.DecisionRequest != nil:
		return schemas.DecisionRequest
	case req.OCRRequest != nil:
		return schemas.OCRRequest
	default:
		return ""
	}
}

func inferMediaFamilyRequestType(req *schemas.BifrostRequest) schemas.RequestType {
	switch {
	case req.SpeechRequest != nil:
		return schemas.SpeechRequest
	case req.TranscriptionRequest != nil:
		return schemas.TranscriptionRequest
	case req.ImageGenerationRequest != nil:
		return schemas.ImageGenerationRequest
	case req.ImageEditRequest != nil:
		return schemas.ImageEditRequest
	case req.ImageVariationRequest != nil:
		return schemas.ImageVariationRequest
	case req.VideoGenerationRequest != nil:
		return schemas.VideoGenerationRequest
	case req.VideoEditRequest != nil:
		return schemas.VideoEditRequest
	case req.VideoRetrieveRequest != nil:
		return schemas.VideoRetrieveRequest
	case req.VideoDownloadRequest != nil:
		return schemas.VideoDownloadRequest
	case req.VideoListRequest != nil:
		return schemas.VideoListRequest
	case req.VideoDeleteRequest != nil:
		return schemas.VideoDeleteRequest
	case req.VideoRemixRequest != nil:
		return schemas.VideoRemixRequest
	default:
		return ""
	}
}

func inferAdminFamilyRequestType(req *schemas.BifrostRequest) schemas.RequestType {
	switch {
	case req.FileUploadRequest != nil:
		return schemas.FileUploadRequest
	case req.FileListRequest != nil:
		return schemas.FileListRequest
	case req.FileRetrieveRequest != nil:
		return schemas.FileRetrieveRequest
	case req.FileDeleteRequest != nil:
		return schemas.FileDeleteRequest
	case req.FileContentRequest != nil:
		return schemas.FileContentRequest
	case req.LiveContentRequest != nil:
		return schemas.LiveContentRequest
	case req.CachedContentCreateRequest != nil:
		return schemas.CachedContentCreateRequest
	case req.CachedContentListRequest != nil:
		return schemas.CachedContentListRequest
	case req.CachedContentRetrieveRequest != nil:
		return schemas.CachedContentRetrieveRequest
	case req.CachedContentUpdateRequest != nil:
		return schemas.CachedContentUpdateRequest
	case req.CachedContentDeleteRequest != nil:
		return schemas.CachedContentDeleteRequest
	case req.BatchCreateRequest != nil:
		return schemas.BatchCreateRequest
	case req.BatchListRequest != nil:
		return schemas.BatchListRequest
	case req.BatchRetrieveRequest != nil:
		return schemas.BatchRetrieveRequest
	case req.BatchCancelRequest != nil:
		return schemas.BatchCancelRequest
	case req.BatchResultsRequest != nil:
		return schemas.BatchResultsRequest
	case req.BatchDeleteRequest != nil:
		return schemas.BatchDeleteRequest
	case req.ContainerCreateRequest != nil:
		return schemas.ContainerCreateRequest
	case req.ContainerListRequest != nil:
		return schemas.ContainerListRequest
	case req.ContainerRetrieveRequest != nil:
		return schemas.ContainerRetrieveRequest
	case req.ContainerDeleteRequest != nil:
		return schemas.ContainerDeleteRequest
	case req.ContainerFileCreateRequest != nil:
		return schemas.ContainerFileCreateRequest
	case req.ContainerFileListRequest != nil:
		return schemas.ContainerFileListRequest
	case req.ContainerFileRetrieveRequest != nil:
		return schemas.ContainerFileRetrieveRequest
	case req.ContainerFileContentRequest != nil:
		return schemas.ContainerFileContentRequest
	case req.ContainerFileDeleteRequest != nil:
		return schemas.ContainerFileDeleteRequest
	case req.PassthroughRequest != nil:
		return schemas.PassthroughRequest
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

func fillLLMIdentityFromContext(llm *schemas.LLMSpanData, bctx *schemas.BifrostContext) {
	if llm == nil || bctx == nil {
		return
	}
	if llm.Provider == "" {
		if p, ok := bctx.Value(schemas.BifrostContextKeyRequestedProvider).(schemas.ModelProvider); ok && p != "" {
			llm.Provider = p
		}
	}
	if llm.RequestModel == "" {
		if m, ok := bctx.Value(schemas.BifrostContextKeyRequestedModel).(string); ok && m != "" {
			llm.RequestModel = m
		}
	}
}

func fillLLMIdentityFromRouting(llm *schemas.LLMSpanData, info schemas.RoutingInfo, requestType schemas.RequestType) {
	if llm == nil {
		return
	}
	if llm.Provider == "" && info.Provider != "" {
		llm.Provider = info.Provider
	}
	if llm.RequestModel == "" {
		switch {
		case info.RequestedModel != "":
			llm.RequestModel = info.RequestedModel
		case info.Model != "":
			llm.RequestModel = info.Model
		}
	}
	if llm.RequestType == "" && requestType != "" {
		llm.RequestType = requestType
	}
}

func fillLLMIdentityFromResponseExtra(llm *schemas.LLMSpanData, ef *schemas.BifrostResponseExtraFields) {
	if llm == nil || ef == nil {
		return
	}
	info := ef.RoutingInfo
	if info.Provider == "" {
		info.Provider = ef.Provider //nolint:staticcheck // SA1019: PopulateExtraFields still writes ExtraFields.Provider.
	}
	fillLLMIdentityFromRouting(llm, info, ef.RequestType)
}

func fillLLMIdentityFromErrorExtra(llm *schemas.LLMSpanData, ef *schemas.BifrostErrorExtraFields) {
	if llm == nil || ef == nil {
		return
	}
	info := ef.RoutingInfo
	if info.Provider == "" {
		info.Provider = ef.Provider //nolint:staticcheck // SA1019: PopulateExtraFields still writes ExtraFields.Provider.
	}
	fillLLMIdentityFromRouting(llm, info, ef.RequestType)
}

// populateLLMResponseUsage fills billed identity and usage on the response
// hook. schemas.Tracer does not pass the request here, so identity comes from
// the span (request hook), BifrostContext, response ExtraFields, or error ExtraFields.
func populateLLMResponseUsage(span *schemas.Span, resp *schemas.BifrostResponse, berr *schemas.BifrostError, bctx *schemas.BifrostContext) {
	if span == nil {
		return
	}
	llm := span.EnsureLLMIfMatch(span.SpanID)
	if llm == nil {
		return
	}
	fillLLMIdentityFromContext(llm, bctx)
	if berr != nil {
		fillLLMIdentityFromErrorExtra(llm, &berr.ExtraFields)
		if llm.Usage == nil {
			llm.Usage = usageWithoutCost(berr.ExtraFields.BilledUsage)
		}
	}
	responseModel := ""
	if resp != nil {
		fillLLMIdentityFromResponseExtra(llm, resp.GetExtraFields())
		responseModel = resp.ServedModel()
		if responseModel != "" {
			llm.ResponseModel = responseModel
		}
		if u := usageWithoutCost(resp.NormalizedUsage()); u != nil {
			llm.Usage = u
		}
		llm.Cost = nil
		setBifrostRenderedAttributes(span, llm.ResponseAttributes())
	} else if llm.Usage != nil {
		llm.Cost = nil
		setBifrostRenderedAttributes(span, llm.ResponseAttributes())
	}
	if llm.Usage != nil {
		overlayOpenInferenceTokenCounts(span, llm.Usage)
	}
	applyLLMIdentityAttributes(span, llm)
	overlayOpenInferenceBilledIdentity(span, llm.Provider, llm.RequestModel, responseModel)
}
