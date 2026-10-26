package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	"github.com/behaviorengineering/polypus/internal/clients/openrouter"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// validateBatchBackend checks that backend can run a batch of endpoint requests for downstream.
// Cloudflare runs chat and embeddings on its batch models, OpenRouter runs chat and
// embeddings, and Claude runs chat only.
func validateBatchBackend(backend config.BackendDef, endpoint, downstream string) error {
	switch {
	case backend.IsAnthropicExtension():
		if endpoint != batch.EndpointChatCompletions {
			return derrors.New(derrors.CodeInvalid, "gateway.validateBatchBackend", "claude batches support /v1/chat/completions only").
				With("endpoint", endpoint)
		}
	case backend.IsCloudflareExtension(), backend.IsOpenRouter():
	default:
		return derrors.New(derrors.CodeFailedPrecondition, "gateway.validateBatchBackend", "batch only supported for cloudflare, openrouter and anthropic backends")
	}
	if !backend.HasCapability(config.CapBatch) {
		return derrors.New(derrors.CodeFailedPrecondition, "gateway.validateBatchBackend", "backend lacks batch capability")
	}
	if backend.IsCloudflareExtension() && !cloudflare.ModelSupportsBatch(downstream) {
		return derrors.New(derrors.CodeInvalid, "gateway.validateBatchBackend", "model does not support cloudflare batch").
			With("model", downstream)
	}
	return nil
}

// submitBatch sends the batch upstream and returns the provider's batch id.
func (h batchesHandler) submitBatch(ctx context.Context, backend config.BackendDef, downstream, endpoint string, lines []batch.InputLine, hop time.Duration) (string, error) {
	if backend.IsAnthropicExtension() {
		return h.router.ClaudeBatchCreate(ctx, backend.ID, downstream, lines, hop)
	}
	if backend.IsOpenRouter() {
		client, err := openrouter.GetClient(backend)
		if err != nil {
			return "", err
		}
		return client.SubmitBatch(ctx, downstream, endpoint, lines)
	}
	client, err := h.cloudflareClient(backend)
	if err != nil {
		return "", err
	}
	return client.SubmitBatch(ctx, downstream, endpoint, lines)
}

// refreshClaudeBatch polls a Claude Message Batch and finalizes it once Anthropic reports it ended.
func (h batchesHandler) refreshClaudeBatch(ctx context.Context, meta batch.BatchMeta, hop time.Duration) (batch.BatchMeta, error) {
	var poll router.BatchPoll
	if err := h.upstreams.Execute(meta.BackendID, func() error {
		var err error
		poll, err = h.router.ClaudeBatchStatus(ctx, meta.BackendID, meta.UpstreamBatchID, hop)
		return err
	}); err != nil {
		return meta, err
	}
	if !poll.Ended {
		if meta.Status == batch.BatchStatusValidating {
			meta.Status = batch.BatchStatusInProgress
			if err := h.batchStore.UpdateBatch(meta); err != nil {
				return meta, err
			}
		}
		return meta, nil
	}
	var results []router.BatchLineResult
	if err := h.upstreams.Execute(meta.BackendID, func() error {
		var err error
		results, err = h.router.ClaudeBatchResults(ctx, meta.BackendID, meta.UpstreamBatchID, meta.PublicModel, hop)
		return err
	}); err != nil {
		return meta, err
	}
	okJSONL, errJSONL, completed, failed := buildBatchLineOutput(results)
	return h.finalizeBatchOutput(meta, okJSONL, errJSONL, completed, failed)
}

// buildBatchLineOutput writes OpenAI batch output and error JSONL from translated lines.
func buildBatchLineOutput(results []router.BatchLineResult) (okJSONL, errJSONL []byte, completed, failed int) {
	var okLines, errLines []string
	for _, item := range results {
		customID := strings.TrimSpace(item.CustomID)
		line := batchOutputLine{ID: "batch_req_" + customID, CustomID: customID}
		if len(item.Body) > 0 && item.ErrCode == "" {
			line.Response = &batchLineResp{StatusCode: http.StatusOK, Body: item.Body}
		} else {
			line.Error = &batchLineError{Code: item.ErrCode, Message: item.ErrMessage}
		}
		b, err := json.Marshal(line)
		if err != nil {
			continue
		}
		if line.Response != nil {
			okLines = append(okLines, string(b))
			completed++
		} else {
			errLines = append(errLines, string(b))
			failed++
		}
	}
	if len(okLines) > 0 {
		okJSONL = []byte(strings.Join(okLines, "\n") + "\n")
	}
	if len(errLines) > 0 {
		errJSONL = []byte(strings.Join(errLines, "\n") + "\n")
	}
	return okJSONL, errJSONL, completed, failed
}

// cancelClaudeBatch asks Anthropic to cancel and marks the batch cancelling; a later refresh
// finalizes it as cancelled with the lines that finished.
func (h batchesHandler) cancelClaudeBatch(r *http.Request, meta batch.BatchMeta) (batch.BatchMeta, error) {
	switch meta.Status {
	case batch.BatchStatusValidating, batch.BatchStatusInProgress:
	case batch.BatchStatusCancelling, batch.BatchStatusCompleted, batch.BatchStatusFailed,
		batch.BatchStatusCancelled, batch.BatchStatusExpired:
		return meta, nil
	default:
		return meta, nil
	}
	ctx := r.Context()
	hop := h.timeouts.ResolveChat(r.Header.Get(config.TimeoutHeader), meta.BackendID, false, false)
	if hop > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, hop)
		defer cancel()
	}
	if err := h.upstreams.Execute(meta.BackendID, func() error {
		return h.router.ClaudeBatchCancel(ctx, meta.BackendID, meta.UpstreamBatchID, hop)
	}); err != nil {
		return meta, err
	}
	meta.Status = batch.BatchStatusCancelling
	if err := h.batchStore.UpdateBatch(meta); err != nil {
		return meta, err
	}
	return meta, nil
}
