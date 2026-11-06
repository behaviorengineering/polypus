// Package service runs OpenAI-shaped batches on the providers that support them
// (Anthropic Message Batches, OpenRouter's Batch API, Workers AI async batch). It owns
// the batch lifecycle: create, refresh, finalise and cancel. The HTTP handlers only
// decode requests and encode responses.
package service

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
	"github.com/behaviorengineering/polypus/internal/observability"
)

// Op names for the service's errors.
const (
	opCreate   = "batch.service.Create"
	opRefresh  = "batch.service.Refresh"
	opFinalize = "batch.service.finalize"
	opCancel   = "batch.service.Cancel"
)

// startSpan starts the span for one lifecycle step on a stored batch.
func startSpan(ctx context.Context, name string, meta batch.Meta) (context.Context, trace.Span) {
	return observability.Tracer().Start(ctx, name, trace.WithAttributes(
		attribute.String("polypus.batch_id", meta.ID),
		attribute.String("polypus.backend_id", meta.BackendID),
	))
}

// Config builds a Service. Every field is required.
type Config struct {
	Store *batch.DiskStore
	// Breakers guards each provider call by backend id.
	Breakers *upstream.Board
	// Now stamps batch records.
	Now func() time.Time
	// Submitter returns the submitter for a backend, or an error when the backend
	// cannot run batches.
	Submitter func(backend config.BackendDef) (Submitter, error)
}

// Service runs the batch lifecycle.
type Service struct {
	cfg Config
}

// CreateService checks the config and returns the service.
func (c Config) CreateService() (*Service, error) {
	if c.Store == nil || c.Breakers == nil || c.Now == nil || c.Submitter == nil {
		return nil, derrors.New(derrors.CodeInvalid, "batch.service", "store, breakers, clock and submitter are required")
	}
	return &Service{cfg: c}, nil
}

// CreateRequest is one batch to create. The handler resolves the backend and the
// public model, and parses the input file into Job.
type CreateRequest struct {
	Backend          config.BackendDef
	PublicModel      string
	InputFileID      string
	CompletionWindow string
	Job              Job
}

// Create stores the batch, submits it once and records the provider's reference. A
// failed submit leaves the batch stored as failed and returns the (typed) cause.
func (s *Service) Create(ctx context.Context, req CreateRequest) (batch.Meta, error) {
	if !req.Backend.HasCapability(config.CapBatch) {
		return batch.Meta{}, derrors.New(derrors.CodeFailedPrecondition, opCreate, "backend lacks batch capability").
			With("backend", req.Backend.ID)
	}
	sub, err := s.cfg.Submitter(req.Backend)
	if err != nil {
		return batch.Meta{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeFailedPrecondition), opCreate, "submitter").
			With("backend", req.Backend.ID)
	}
	if err := sub.Validate(req.Job); err != nil {
		return batch.Meta{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), opCreate, "validate").
			With("backend", req.Backend.ID)
	}
	now := s.cfg.Now().Unix()
	expiresAt, err := batch.ExpiresAtUnix(now, req.CompletionWindow)
	if err != nil {
		return batch.Meta{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), opCreate, "completion window")
	}
	meta := batch.Meta{
		Endpoint:         req.Job.Endpoint,
		InputFileID:      req.InputFileID,
		CompletionWindow: req.CompletionWindow,
		Status:           batch.StatusValidating,
		BackendID:        req.Backend.ID,
		PublicModel:      req.PublicModel,
		RequestCounts:    batch.RequestCounts{Total: len(req.Job.Lines)},
		CreatedAt:        now,
		ExpiresAt:        expiresAt,
	}
	if err := s.cfg.Store.SaveBatch(&meta); err != nil {
		return batch.Meta{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInternal), opCreate, "save").
			With("backend", req.Backend.ID)
	}
	submitErr := s.cfg.Breakers.Execute(req.Backend.ID, func() error {
		return sub.Submit(ctx, req.Job, &meta)
	})
	if submitErr != nil {
		meta.Status = batch.StatusFailed
		meta.FailedAt = s.cfg.Now().Unix()
		if err := s.cfg.Store.UpdateBatch(meta); err != nil {
			// Keep the provider's rejection: it is why the batch failed.
			return meta, derrors.Wrap(errors.Join(submitErr, err), derrors.CodeOr(submitErr, derrors.CodeUnavailable), opCreate, "submit, then save failed status").
				With("backend", req.Backend.ID).With("batch_id", meta.ID)
		}
		return meta, derrors.Wrap(submitErr, derrors.CodeOr(submitErr, derrors.CodeUnavailable), opCreate, "submit").
			With("backend", req.Backend.ID).With("batch_id", meta.ID)
	}
	meta.Status = batch.StatusInProgress
	meta.InProgressAt = s.cfg.Now().Unix()
	if err := s.cfg.Store.UpdateBatch(meta); err != nil {
		return meta, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInternal), opCreate, "save in progress").
			With("batch_id", meta.ID)
	}
	return meta, nil
}

// Refresh brings a stored batch up to date: it expires one past its window, polls the
// provider, and finalises the output once the provider is done. A terminal batch is
// returned as it is.
func (s *Service) Refresh(ctx context.Context, backend config.BackendDef, meta batch.Meta) (_ batch.Meta, err error) {
	if meta.Status.Terminal() {
		return meta, nil
	}
	ctx, span := startSpan(ctx, "polypus.batch.refresh", meta)
	defer func() { observability.EndSpan(span, err) }()
	if meta.ExpiresAt > 0 && s.cfg.Now().Unix() > meta.ExpiresAt && meta.Status != batch.StatusCancelling {
		meta.Status = batch.StatusExpired
		return meta, s.save(opRefresh, meta)
	}
	sub, err := s.cfg.Submitter(backend)
	if err != nil {
		return meta, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeFailedPrecondition), opRefresh, "submitter").
			With("batch_id", meta.ID)
	}
	var poll Poll
	if err = s.cfg.Breakers.Execute(meta.BackendID, func() error {
		var perr error
		poll, perr = sub.Poll(ctx, meta)
		return perr
	}); err != nil {
		return meta, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), opRefresh, "poll").
			With("batch_id", meta.ID)
	}
	now := s.cfg.Now().Unix()
	switch poll.State {
	case pollUnknown:
		return meta, derrors.New(derrors.CodeInternal, opRefresh, "submitter returned no poll state").
			With("batch_id", meta.ID)
	case PollDone:
		return s.finalize(meta, poll.Lines)
	case PollFailed:
		meta.Status = batch.StatusFailed
		meta.FailedAt = now
	case PollExpired:
		meta.Status = batch.StatusExpired
	case PollCancelled:
		meta.Status = batch.StatusCancelled
		meta.CancelledAt = now
	case PollRunning:
		if meta.Status != batch.StatusValidating {
			return meta, nil
		}
		meta.Status = batch.StatusInProgress
	}
	return meta, s.save(opRefresh, meta)
}

// save stores meta, naming op and the batch on failure.
func (s *Service) save(op string, meta batch.Meta) error {
	if err := s.cfg.Store.UpdateBatch(meta); err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInternal), op, "save").With("batch_id", meta.ID)
	}
	return nil
}

// finalize writes the output files and the terminal status. A batch that was
// cancelling ends cancelled and keeps the lines that finished.
func (s *Service) finalize(meta batch.Meta, lines []batch.LineResult) (batch.Meta, error) {
	okJSONL, errJSONL, completed, failed := batch.BuildOutput(lines)
	now := s.cfg.Now().Unix()
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
	case meta.Status == batch.StatusCancelling:
		patch.Status = batch.StatusCancelled
		patch.CancelledAt = now
	case failed > 0 && completed == 0:
		patch.Status = batch.StatusFailed
		patch.FailedAt = now
	default:
		patch.Status = batch.StatusCompleted
		patch.CompletedAt = now
	}
	updated, applied, err := s.cfg.Store.TryFinalizeBatch(meta.ID, patch)
	if err != nil {
		return meta, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInternal), opFinalize, "finalize").With("batch_id", meta.ID)
	}
	if !applied {
		// Another request finalised it first; return what it wrote.
		stored, err := s.cfg.Store.GetBatch(meta.ID)
		if err != nil {
			return meta, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInternal), opFinalize, "reload").With("batch_id", meta.ID)
		}
		return stored, nil
	}
	return updated, nil
}

// Cancel asks the provider to cancel a running batch and marks it cancelling; a later
// Refresh finalises it with the lines that finished. A batch that is not running is
// returned as it is.
func (s *Service) Cancel(ctx context.Context, backend config.BackendDef, meta batch.Meta) (_ batch.Meta, err error) {
	if meta.Status != batch.StatusValidating && meta.Status != batch.StatusInProgress {
		return meta, nil
	}
	ctx, span := startSpan(ctx, "polypus.batch.cancel", meta)
	defer func() { observability.EndSpan(span, err) }()
	sub, err := s.cfg.Submitter(backend)
	if err != nil {
		return meta, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeFailedPrecondition), opCancel, "submitter").
			With("batch_id", meta.ID)
	}
	if err = s.cfg.Breakers.Execute(meta.BackendID, func() error {
		return sub.Cancel(ctx, meta)
	}); err != nil {
		return meta, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), opCancel, "cancel").
			With("batch_id", meta.ID)
	}
	meta.Status = batch.StatusCancelling
	return meta, s.save(opCancel, meta)
}
