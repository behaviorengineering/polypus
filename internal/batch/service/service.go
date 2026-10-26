// Package service runs OpenAI-shaped batches on the providers that support them
// (Anthropic Message Batches, OpenRouter's Batch API, Workers AI async batch). It owns
// the batch lifecycle: create, refresh, finalise and cancel. The HTTP handlers only
// decode requests and encode responses.
package service

import (
	"context"
	"time"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
)

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
	const op = "batch.service.Create"
	if !req.Backend.HasCapability(config.CapBatch) {
		return batch.Meta{}, derrors.New(derrors.CodeFailedPrecondition, op, "backend lacks batch capability").
			With("backend", req.Backend.ID)
	}
	sub, err := s.cfg.Submitter(req.Backend)
	if err != nil {
		return batch.Meta{}, err
	}
	if err := sub.Validate(req.Job); err != nil {
		return batch.Meta{}, err
	}
	now := s.cfg.Now().Unix()
	expiresAt, err := batch.ExpiresAtUnix(now, req.CompletionWindow)
	if err != nil {
		return batch.Meta{}, err
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
		return batch.Meta{}, err
	}
	submitErr := s.cfg.Breakers.Execute(req.Backend.ID, func() error {
		return sub.Submit(ctx, req.Job, &meta)
	})
	if submitErr != nil {
		meta.Status = batch.StatusFailed
		meta.FailedAt = s.cfg.Now().Unix()
		if err := s.cfg.Store.UpdateBatch(meta); err != nil {
			return meta, err
		}
		return meta, derrors.Wrap(submitErr, derrors.CodeOr(submitErr, derrors.CodeUnavailable), op, "submit").
			With("backend", req.Backend.ID)
	}
	meta.Status = batch.StatusInProgress
	meta.InProgressAt = s.cfg.Now().Unix()
	if err := s.cfg.Store.UpdateBatch(meta); err != nil {
		return meta, err
	}
	return meta, nil
}

// Refresh brings a stored batch up to date: it expires one past its window, polls the
// provider, and finalises the output once the provider is done. A terminal batch is
// returned as it is.
func (s *Service) Refresh(ctx context.Context, backend config.BackendDef, meta batch.Meta) (batch.Meta, error) {
	if terminal(meta.Status) {
		return meta, nil
	}
	if meta.ExpiresAt > 0 && s.cfg.Now().Unix() > meta.ExpiresAt && meta.Status != batch.StatusCancelling {
		meta.Status = batch.StatusExpired
		return meta, s.cfg.Store.UpdateBatch(meta)
	}
	sub, err := s.cfg.Submitter(backend)
	if err != nil {
		return meta, err
	}
	var poll Poll
	if err := s.cfg.Breakers.Execute(meta.BackendID, func() error {
		var perr error
		poll, perr = sub.Poll(ctx, meta)
		return perr
	}); err != nil {
		return meta, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), "batch.service.Refresh", "poll").
			With("batch_id", meta.ID)
	}
	now := s.cfg.Now().Unix()
	switch poll.State {
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
	return meta, s.cfg.Store.UpdateBatch(meta)
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
		return meta, err
	}
	if !applied {
		// Another request finalised it first; return what it wrote.
		return s.cfg.Store.GetBatch(meta.ID)
	}
	return updated, nil
}

// Cancel asks the provider to cancel a running batch and marks it cancelling; a later
// Refresh finalises it with the lines that finished. A batch that is not running is
// returned as it is.
func (s *Service) Cancel(ctx context.Context, backend config.BackendDef, meta batch.Meta) (batch.Meta, error) {
	if meta.Status != batch.StatusValidating && meta.Status != batch.StatusInProgress {
		return meta, nil
	}
	sub, err := s.cfg.Submitter(backend)
	if err != nil {
		return meta, err
	}
	if err := s.cfg.Breakers.Execute(meta.BackendID, func() error {
		return sub.Cancel(ctx, meta)
	}); err != nil {
		return meta, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), "batch.service.Cancel", "cancel").
			With("batch_id", meta.ID)
	}
	meta.Status = batch.StatusCancelling
	return meta, s.cfg.Store.UpdateBatch(meta)
}

func terminal(status batch.Status) bool {
	switch status {
	case batch.StatusCompleted, batch.StatusFailed, batch.StatusCancelled, batch.StatusExpired:
		return true
	case batch.StatusValidating, batch.StatusInProgress, batch.StatusCancelling:
		return false
	default:
		return false
	}
}
