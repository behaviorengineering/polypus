package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/batches"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
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
		h.writeHandlerError(w, derrors.New(derrors.CodeNotReady, "gateway.serveBatches", "batch store not configured"))
		return
	}
	switch r.Method {
	case http.MethodPost:
		h.serveBatchCreate(w, r)
	case http.MethodGet:
		h.serveBatchList(w, r)
	default:
		h.writeMethodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (h batchesHandler) serveBatchByID(w http.ResponseWriter, r *http.Request) {
	if h.batchStore == nil {
		h.writeHandlerError(w, derrors.New(derrors.CodeNotReady, "gateway.serveBatchByID", "batch store not configured"))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, pathBatchesPrefix)
	path = strings.Trim(path, "/")
	if strings.HasSuffix(path, "/cancel") {
		id := strings.TrimSuffix(path, "/cancel")
		id = strings.Trim(id, "/")
		if r.Method == http.MethodPost {
			h.serveBatchCancel(w, r, id)
			return
		}
		h.writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	h.serveBatchRetrieve(w, r, path)
}

func (h batchesHandler) serveBatchCreate(w http.ResponseWriter, r *http.Request) {
	const op = "gateway.serveBatchCreate"
	reg := h.registry
	if reg.Config().EffectiveBatchBackend() == "" {
		h.writeHandlerError(w, derrors.New(derrors.CodeNotReady, op, "no batch backend configured"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, batchMaxInputBytes))
	if err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, op, "read body"))
		return
	}
	var req createBatchRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, op, "parse json"))
		return
	}
	if strings.TrimSpace(req.CompletionWindow) == "" {
		req.CompletionWindow = "24h"
	}
	inputID := strings.TrimSpace(req.InputFileID)
	if inputID == "" {
		h.writeHandlerError(w, derrors.New(derrors.CodeInvalid, op, "input_file_id required"))
		return
	}
	raw, err := h.batchStore.ReadFileContent(inputID)
	if err != nil {
		h.writeHandlerError(w, err)
		return
	}
	parsed, err := batch.ParseBatchJSONL(raw, req.Endpoint)
	if err != nil {
		h.writeHandlerError(w, err)
		return
	}
	backendID, downstream, err := reg.ResolveBatch(parsed.Model)
	if err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, op, "resolve backend"))
		return
	}
	publicModel := parsed.Model
	if !strings.Contains(publicModel, "/") {
		publicModel = prefixModelID(backendID, downstream)
	}
	if !h.ensureModelAllowed(backendID, publicModel) {
		h.writeModelNotAllowed(w, publicModel)
		return
	}
	backend, ok := reg.Backend(backendID)
	if !ok {
		h.writeHandlerError(w, derrors.New(derrors.CodeUnavailable, op, msgBackendNotFound).With("backend", backendID))
		return
	}

	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.batch.create", observability.LLMSpanAttrs{
		Model: publicModel, BackendID: backendID, BackendURL: backend.BaseURL, Downstream: downstream,
	})
	var createErr error
	defer func() { endDialSpan(span, createErr, backendID) }()
	ctx, cancel := h.batchHopContext(ctx, r.Header.Get(config.TimeoutHeader), backendID)
	defer cancel()
	meta, createErr := h.batches.Create(ctx, batches.CreateRequest{
		Backend:          backend,
		PublicModel:      publicModel,
		InputFileID:      inputID,
		CompletionWindow: req.CompletionWindow,
		Job:              batches.Job{Downstream: downstream, Endpoint: req.Endpoint, Lines: parsed.Lines},
	})
	if createErr != nil {
		h.writeUpstreamDialError(w, dialFailure{err: createErr, upstream: backendID})
		return
	}
	h.writeJSON(w, http.StatusOK, meta.Public())
}

// batchHopContext bounds one batch call to the provider by the chat hop budget.
// timeoutHeader is the caller's requested timeout, if any.
func (h batchesHandler) batchHopContext(ctx context.Context, timeoutHeader, backendID string) (context.Context, context.CancelFunc) {
	hop := h.timeouts.ResolveChat(timeoutHeader, backendID, config.ChatPlain)
	return context.WithTimeout(ctx, hopBudget(hop, config.DefaultTimeouts().Chat))
}

func (h batchesHandler) serveBatchRetrieve(w http.ResponseWriter, r *http.Request, id string) {
	meta, err := h.batchStore.GetBatch(id)
	if err != nil {
		h.writeHandlerError(w, err)
		return
	}
	backend, ok := h.registry.Backend(meta.BackendID)
	if !ok && !meta.Status.Terminal() {
		h.writeHandlerError(w, derrors.New(derrors.CodeUnavailable, "gateway.serveBatchRetrieve", msgBackendNotFound).
			With("backend", meta.BackendID))
		return
	}
	ctx, cancel := h.batchHopContext(r.Context(), r.Header.Get(config.TimeoutHeader), meta.BackendID)
	defer cancel()
	updated, err := h.batches.Refresh(ctx, backend, meta)
	if err != nil {
		if isBatchRefreshMiss(err) {
			h.log.Warn("polypus: batch refresh failed; returning last-known", "batch_id", meta.ID, "err", err)
			h.writeJSON(w, http.StatusOK, meta.Public())
			return
		}
		h.writeHandlerError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, updated.Public())
}

func (h batchesHandler) serveBatchList(w http.ResponseWriter, r *http.Request) {
	items, err := h.batchStore.ListBatches()
	if err != nil {
		h.writeHandlerError(w, err)
		return
	}
	pub := make([]batch.PublicBatch, 0, len(items))
	for _, m := range items {
		pub = append(pub, m.Public())
	}
	h.writeJSON(w, http.StatusOK, batchListResponse{Object: objectList, Data: pub})
}

func (h batchesHandler) serveBatchCancel(w http.ResponseWriter, r *http.Request, id string) {
	meta, err := h.batchStore.GetBatch(id)
	if err != nil {
		h.writeHandlerError(w, err)
		return
	}
	backend, ok := h.registry.Backend(meta.BackendID)
	if !ok {
		h.writeHandlerError(w, derrors.New(derrors.CodeUnavailable, "gateway.serveBatchCancel", msgBackendNotFound).
			With("backend", meta.BackendID))
		return
	}
	ctx, cancel := h.batchHopContext(r.Context(), r.Header.Get(config.TimeoutHeader), meta.BackendID)
	defer cancel()
	updated, err := h.batches.Cancel(ctx, backend, meta)
	if err != nil {
		h.writeUpstreamDialError(w, dialFailure{err: err, upstream: meta.BackendID})
		return
	}
	h.writeJSON(w, http.StatusOK, updated.Public())
}

// isBatchRefreshMiss reports a poll failure that should return the last-known batch
// instead of an error: the provider is unreachable, slow or rate limiting.
func isBatchRefreshMiss(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, derrors.ErrUnavailable) ||
		errors.Is(err, derrors.ErrTimeout) ||
		errors.Is(err, derrors.ErrRateLimited) {
		return true
	}
	return upstream.Unavailable(err)
}
