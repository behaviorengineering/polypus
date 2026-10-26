package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/observability"
)

type batchesHandler struct{ *shared }

type createBatchRequest struct {
	InputFileID      string `json:"input_file_id"`
	Endpoint         string `json:"endpoint"`
	CompletionWindow string `json:"completion_window"`
}

type batchListResponse struct {
	Object string              `json:"object"`
	Data   []batch.PublicBatch `json:"data"`
}

func (h batchesHandler) serveBatches(w http.ResponseWriter, r *http.Request) {
	if h.batchStore == nil {
		writeHandlerError(w, derrors.New(derrors.CodeNotReady, "gateway.serveBatches", "batch store not configured"))
		return
	}
	switch r.Method {
	case http.MethodPost:
		h.serveBatchCreate(w, r)
	case http.MethodGet:
		h.serveBatchList(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h batchesHandler) serveBatchByID(w http.ResponseWriter, r *http.Request) {
	if h.batchStore == nil {
		writeHandlerError(w, derrors.New(derrors.CodeNotReady, "gateway.serveBatchByID", "batch store not configured"))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/batches/")
	path = strings.Trim(path, "/")
	if strings.HasSuffix(path, "/cancel") {
		id := strings.TrimSuffix(path, "/cancel")
		id = strings.Trim(id, "/")
		if r.Method == http.MethodPost {
			h.serveBatchCancel(w, r, id)
			return
		}
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	h.serveBatchRetrieve(w, r, path)
}

func (h batchesHandler) serveBatchCreate(w http.ResponseWriter, r *http.Request) {
	reg := h.router.Registry()
	cfg := reg.Config()
	if cfg.EffectiveBatchBackend() == "" {
		writeHandlerError(w, derrors.New(derrors.CodeNotReady, "gateway.serveBatchCreate", "no batch backend configured"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, batchMaxInputBytes))
	if err != nil {
		writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveBatchCreate", "read body"))
		return
	}
	var req createBatchRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveBatchCreate", "parse json"))
		return
	}
	if strings.TrimSpace(req.CompletionWindow) == "" {
		req.CompletionWindow = "24h"
	}
	inputID := strings.TrimSpace(req.InputFileID)
	if inputID == "" {
		writeHandlerError(w, derrors.New(derrors.CodeInvalid, "gateway.serveBatchCreate", "input_file_id required"))
		return
	}
	raw, err := h.batchStore.ReadFileContent(inputID)
	if err != nil {
		writeHandlerError(w, err)
		return
	}
	parsed, err := batch.ParseBatchJSONL(raw, req.Endpoint)
	if err != nil {
		writeHandlerError(w, err)
		return
	}
	backendID, downstream, err := reg.ResolveBatch(parsed.Model)
	if err != nil {
		writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveBatchCreate", "resolve backend"))
		return
	}
	publicModel := parsed.Model
	if !strings.Contains(publicModel, "/") {
		publicModel = prefixModelID(backendID, downstream)
	}
	if !h.ensureModelAllowed(backendID, publicModel) {
		writeModelNotAllowed(w, publicModel)
		return
	}
	backend, ok := reg.Backend(backendID)
	if !ok {
		writeHandlerError(w, derrors.New(derrors.CodeUnavailable, "gateway.serveBatchCreate", errBackendNotFound).
			With("backend", backendID))
		return
	}
	if err := validateBatchBackend(backend, req.Endpoint, downstream); err != nil {
		writeHandlerError(w, err)
		return
	}
	if backend.IsOpenRouter() {
		if err := rejectInlineMedia(parsed.Lines); err != nil {
			writeHandlerError(w, err)
			return
		}
	}
	now := h.batchNowTime().Unix()
	expiresAt, err := batch.ExpiresAtUnix(now, req.CompletionWindow)
	if err != nil {
		writeHandlerError(w, err)
		return
	}
	meta := &batch.BatchMeta{
		Endpoint:         req.Endpoint,
		InputFileID:      inputID,
		CompletionWindow: req.CompletionWindow,
		Status:           batch.BatchStatusValidating,
		BackendID:        backendID,
		PublicModel:      publicModel,
		RequestCounts: batch.RequestCounts{
			Total: len(parsed.Lines),
		},
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}
	if err := h.batchStore.SaveBatch(meta); err != nil {
		writeHandlerError(w, err)
		return
	}

	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.batch.create", publicModel, backendID, backend.BaseURL, downstream)
	var submitErr error
	dialUpstream := backendID
	defer func() { observability.EndDialSpan(span, submitErr, dialUpstream) }()
	hop := h.timeouts.ResolveChat(r.Header.Get(config.TimeoutHeader), backendID, false, false)
	hop = hopBudget(hop, config.DefaultTimeouts().Chat)
	ctx, cancel := context.WithTimeout(ctx, hop)
	defer cancel()
	if backend.IsCloudflareExtension() {
		meta.CFModel = cloudflare.NormalizeModel(downstream)
	}
	var upstreamID string
	submitErr = h.upstreams.Execute(backendID, func() error {
		var err error
		upstreamID, err = h.submitBatch(ctx, backend, downstream, req.Endpoint, parsed.Lines, hop)
		return err
	})
	if submitErr != nil {
		meta.Status = batch.BatchStatusFailed
		meta.FailedAt = h.batchNowTime().Unix()
		if err := h.batchStore.UpdateBatch(*meta); err != nil {
			writeHandlerError(w, err)
			return
		}
		writeUpstreamDialError(w, submitErr, "", backendID, nil)
		return
	}
	if backend.IsCloudflareExtension() {
		meta.CFRequestID = upstreamID
	} else {
		meta.UpstreamBatchID = upstreamID
	}
	meta.Status = batch.BatchStatusInProgress
	meta.InProgressAt = h.batchNowTime().Unix()
	if err := h.batchStore.UpdateBatch(*meta); err != nil {
		writeHandlerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meta.Public())
}

func (h batchesHandler) serveBatchRetrieve(w http.ResponseWriter, r *http.Request, id string) {
	meta, err := h.batchStore.GetBatch(id)
	if err != nil {
		writeHandlerError(w, err)
		return
	}
	if meta.Status == batch.BatchStatusCancelled || meta.Status == batch.BatchStatusCompleted || meta.Status == batch.BatchStatusFailed || meta.Status == batch.BatchStatusExpired {
		writeJSON(w, http.StatusOK, meta.Public())
		return
	}
	meta, expired, err := h.applyBatchExpiry(meta)
	if err != nil {
		writeHandlerError(w, err)
		return
	}
	if expired {
		writeJSON(w, http.StatusOK, meta.Public())
		return
	}
	meta, err = h.refreshBatch(r, meta)
	if err != nil {
		if isBatchRefreshMiss(err) {
			slog.Warn("polypus: batch refresh failed; returning last-known", "batch_id", meta.ID, "err", err)
			writeJSON(w, http.StatusOK, meta.Public())
			return
		}
		writeHandlerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meta.Public())
}

func (h batchesHandler) applyBatchExpiry(meta batch.BatchMeta) (batch.BatchMeta, bool, error) {
	if meta.ExpiresAt <= 0 || h.batchNowTime().Unix() <= meta.ExpiresAt {
		return meta, false, nil
	}
	switch meta.Status {
	case batch.BatchStatusValidating, batch.BatchStatusInProgress:
		meta.Status = batch.BatchStatusExpired
		if err := h.batchStore.UpdateBatch(meta); err != nil {
			return meta, false, err
		}
		return meta, true, nil
	case batch.BatchStatusCancelling, batch.BatchStatusCompleted, batch.BatchStatusFailed,
		batch.BatchStatusCancelled, batch.BatchStatusExpired:
		return meta, false, nil
	default:
		return meta, false, nil
	}
}

// refreshBatch polls the upstream batch for its backend and finalizes it once finished.
func (h batchesHandler) refreshBatch(r *http.Request, meta batch.BatchMeta) (batch.BatchMeta, error) {
	if meta.Status == batch.BatchStatusCancelled {
		return meta, nil
	}
	backend, ok := h.router.Registry().Backend(meta.BackendID)
	if !ok {
		return meta, derrors.New(derrors.CodeUnavailable, "gateway.refreshBatch", errBackendNotFound)
	}
	ctx := r.Context()
	hop := h.timeouts.ResolveChat(r.Header.Get(config.TimeoutHeader), meta.BackendID, false, false)
	hop = hopBudget(hop, config.DefaultTimeouts().Chat)
	ctx, cancel := context.WithTimeout(ctx, hop)
	defer cancel()
	if backend.IsAnthropicExtension() {
		return h.refreshClaudeBatch(ctx, meta, hop)
	}
	if backend.IsOpenRouter() {
		return h.refreshOpenRouterBatch(ctx, backend, meta)
	}
	return h.refreshBatchFromCloudflare(ctx, backend, meta)
}

func (h batchesHandler) refreshBatchFromCloudflare(ctx context.Context, backend config.BackendDef, meta batch.BatchMeta) (batch.BatchMeta, error) {
	var poll cloudflare.BatchPollResult
	err := h.upstreams.Execute(meta.BackendID, func() error {
		client, err := h.cloudflareClient(backend)
		if err != nil {
			return err
		}
		poll, err = client.PollBatch(ctx, meta.CFModel, meta.CFRequestID)
		return err
	})
	if err != nil {
		return meta, err
	}
	switch poll.State {
	case cloudflare.BatchPollQueued, cloudflare.BatchPollRunning:
		meta.Status = batch.BatchStatusInProgress
		if err := h.batchStore.UpdateBatch(meta); err != nil {
			return meta, err
		}
		return meta, nil
	case cloudflare.BatchPollFailed:
		now := h.batchNowTime().Unix()
		meta.Status = batch.BatchStatusFailed
		meta.FailedAt = now
		if err := h.batchStore.UpdateBatch(meta); err != nil {
			return meta, err
		}
		return meta, nil
	case cloudflare.BatchPollCompleted:
		return h.finalizeBatch(meta, poll)
	default:
		return meta, nil
	}
}

func (h batchesHandler) finalizeBatch(meta batch.BatchMeta, poll cloudflare.BatchPollResult) (batch.BatchMeta, error) {
	createdUnix := h.batchNowTime().Unix()
	okJSONL, errJSONL, completed, failed := buildBatchLineOutput(
		cloudflareBatchLines(meta.Endpoint, meta.PublicModel, createdUnix, poll.Responses))
	return h.finalizeBatchOutput(meta, okJSONL, errJSONL, completed, failed)
}

// finalizeBatchOutput writes the output files and the terminal status. A batch that was
// cancelling ends cancelled and keeps the lines that finished.
func (h batchesHandler) finalizeBatchOutput(meta batch.BatchMeta, okJSONL, errJSONL []byte, completed, failed int) (batch.BatchMeta, error) {
	createdUnix := h.batchNowTime().Unix()
	patch := batch.FinalizePatch{
		OkJSONL:  okJSONL,
		ErrJSONL: errJSONL,
		RequestCounts: batch.RequestCounts{
			Total:     meta.RequestCounts.Total,
			Completed: completed,
			Failed:    failed,
		},
	}
	switch {
	case meta.Status == batch.BatchStatusCancelling:
		patch.Status = batch.BatchStatusCancelled
		patch.CancelledAt = createdUnix
	case failed > 0 && completed == 0:
		patch.Status = batch.BatchStatusFailed
		patch.FailedAt = createdUnix
	default:
		patch.Status = batch.BatchStatusCompleted
		patch.CompletedAt = createdUnix
	}
	updated, applied, err := h.batchStore.TryFinalizeBatch(meta.ID, patch)
	if err != nil {
		return meta, err
	}
	if !applied {
		current, err := h.batchStore.GetBatch(meta.ID)
		if err != nil {
			return meta, err
		}
		return current, nil
	}
	return updated, nil
}

func (h batchesHandler) serveBatchList(w http.ResponseWriter, r *http.Request) {
	items, err := h.batchStore.ListBatches()
	if err != nil {
		writeHandlerError(w, err)
		return
	}
	pub := make([]batch.PublicBatch, 0, len(items))
	for _, m := range items {
		pub = append(pub, m.Public())
	}
	writeJSON(w, http.StatusOK, batchListResponse{Object: "list", Data: pub})
}

func (h batchesHandler) serveBatchCancel(w http.ResponseWriter, r *http.Request, id string) {
	meta, err := h.batchStore.GetBatch(id)
	if err != nil {
		writeHandlerError(w, err)
		return
	}
	backend, ok := h.router.Registry().Backend(meta.BackendID)
	if !ok || !backend.IsAnthropicExtension() {
		writeHandlerError(w, derrors.New(derrors.CodeUnimplemented, "gateway.serveBatchCancel", "batch cancel is not supported for this backend (cloudflare workers ai and openrouter have no cancel)"))
		return
	}
	meta, err = h.cancelClaudeBatch(r, meta)
	if err != nil {
		writeUpstreamDialError(w, err, "", meta.BackendID, nil)
		return
	}
	writeJSON(w, http.StatusOK, meta.Public())
}
