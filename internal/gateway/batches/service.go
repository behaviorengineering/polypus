// Package batches runs OpenAI-shaped batches on the providers that support them
// (Anthropic Message Batches, OpenRouter's Batch API, Workers AI async batch). It owns
// the batch lifecycle: create, refresh, finalise and cancel. The HTTP handlers only
// decode requests and encode responses.
package batches

import (
	"context"
	"errors"
	"log/slog"
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
	opCreate   = "batches.Create"
	opRefresh  = "batches.Refresh"
	opFinalize = "batches.finalize"
	opCancel   = "batches.Cancel"
)

// batchOrphanCancelTimeout bounds the cancel Create sends when the provider accepted a
// batch the store then failed to record. It runs after the caller's ctx may be done,
// so it needs a budget of its own.
const batchOrphanCancelTimeout = 10 * time.Second

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
	// Logger receives the warnings and errors of best-effort upstream cancels.
	Logger *slog.Logger
}

// Service runs the batch lifecycle.
type Service struct {
	cfg Config
}

// CreateService checks the config and returns the service.
func (c Config) CreateService() (*Service, error) {
	if c.Store == nil || c.Breakers == nil || c.Now == nil || c.Submitter == nil || c.Logger == nil {
		return nil, derrors.New(derrors.CodeInvalid, "batches", "store, breakers, clock, submitter and logger are required")
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
// failed submit leaves the batch stored as failed and returns the (typed) cause. When
// the provider accepts the batch but the store cannot record it, Create cancels it
// upstream (see cancelOrphan) and returns the save error.
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
	if err := s.cfg.Store.SaveBatch(ctx, &meta); err != nil {
		return batch.Meta{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInternal), opCreate, "save").
			With("backend", req.Backend.ID)
	}
	submitErr := s.cfg.Breakers.Execute(ctx, req.Backend.ID, func() error {
		return sub.Submit(ctx, req.Job, &meta)
	})
	// From here the provider has answered, so the record of that answer is saved even
	// when the caller has gone away meanwhile.
	saveCtx := context.WithoutCancel(ctx)
	if submitErr != nil {
		meta.Status = batch.StatusFailed
		meta.FailedAt = s.cfg.Now().Unix()
		if err := s.cfg.Store.UpdateBatch(saveCtx, meta); err != nil {
			// Keep the provider's rejection: it is why the batch failed.
			return meta, derrors.Wrap(errors.Join(submitErr, err), derrors.CodeOr(submitErr, derrors.CodeUnavailable), opCreate, "submit, then save failed status").
				With("backend", req.Backend.ID).With("batch_id", meta.ID)
		}
		return meta, derrors.Wrap(submitErr, derrors.CodeOr(submitErr, derrors.CodeUnavailable), opCreate, "submit").
			With("backend", req.Backend.ID).With("batch_id", meta.ID)
	}
	meta.Status = batch.StatusInProgress
	meta.InProgressAt = s.cfg.Now().Unix()
	if err := s.cfg.Store.UpdateBatch(saveCtx, meta); err != nil {
		return meta, s.cancelOrphan(ctx, sub, meta, err)
	}
	return meta, nil
}

// cancelOrphan stops a batch the provider accepted but the store failed to record.
// Nothing on disk points at it, so nothing would poll, collect or cancel it, and a
// caller retry would pay for a second one. The caller's ctx may be done, so the
// cancel runs on its own budget. When the cancel fails, or the provider cannot cancel,
// the upstream reference is logged at Error so an operator can recover it by hand.
// The returned error keeps saveErr's code.
func (s *Service) cancelOrphan(ctx context.Context, sub Submitter, meta batch.Meta, saveErr error) error {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), batchOrphanCancelTimeout)
	defer cancel()
	cancelErr := s.cancelUpstream(cctx, sub, meta)
	msg := "save in progress, upstream cancelled"
	if cancelErr != nil {
		msg = "save in progress, upstream not cancelled"
		s.cfg.Logger.Error("polypus: batch submitted but not saved, and cancel failed; recover by hand",
			"batch_id", meta.ID, "backend", meta.BackendID, "upstream_batch_id", meta.UpstreamBatchID,
			"cf_request_id", meta.CFRequestID, "err", cancelErr)
	}
	return derrors.Wrap(errors.Join(saveErr, cancelErr), derrors.CodeOr(saveErr, derrors.CodeInternal), opCreate, msg).
		With("batch_id", meta.ID)
}

// cancelUpstream asks the provider to cancel meta under the backend's breaker. A
// provider that cannot cancel answers CodeUnimplemented: that is returned to the
// caller but not counted against the breaker, since the upstream is not failing.
func (s *Service) cancelUpstream(ctx context.Context, sub Submitter, meta batch.Meta) error {
	return s.cfg.Breakers.Execute(ctx, meta.BackendID, func() error {
		return sub.Cancel(ctx, meta)
	})
}

// cancelExpired asks the provider to stop a batch Polypus has expired on its own
// clock, since its results will no longer be collected. It is best effort and never
// blocks the expiry: a batch with no upstream reference has nothing to cancel, a
// provider that cannot cancel (CodeUnimplemented) is skipped quietly, and any other
// failure is logged and recorded on the span.
func (s *Service) cancelExpired(ctx context.Context, backend config.BackendDef, meta batch.Meta) {
	if meta.UpstreamBatchID == "" && meta.CFRequestID == "" {
		return
	}
	sub, err := s.cfg.Submitter(backend)
	if err == nil {
		err = s.cancelUpstream(ctx, sub, meta)
	}
	if err == nil || derrors.CodeOf(err) == derrors.CodeUnimplemented {
		return
	}
	trace.SpanFromContext(ctx).RecordError(err)
	s.cfg.Logger.Warn("polypus: cancel of expired batch failed",
		"batch_id", meta.ID, "backend", meta.BackendID, "upstream_batch_id", meta.UpstreamBatchID,
		"cf_request_id", meta.CFRequestID, "err", err)
}

// Refresh brings a stored batch up to date: it expires one past its window (asking the
// provider to cancel it, best effort), polls the provider, and finalises the output
// once the provider is done. A terminal batch is returned as it is.
func (s *Service) Refresh(ctx context.Context, backend config.BackendDef, meta batch.Meta) (_ batch.Meta, err error) {
	if meta.Status.Terminal() {
		return meta, nil
	}
	ctx, span := startSpan(ctx, "polypus.batch.refresh", meta)
	defer func() { observability.EndSpan(span, err) }()
	if meta.ExpiresAt > 0 && s.cfg.Now().Unix() > meta.ExpiresAt && meta.Status != batch.StatusCancelling {
		s.cancelExpired(ctx, backend, meta)
		meta.Status = batch.StatusExpired
		return meta, s.save(ctx, opRefresh, meta)
	}
	sub, err := s.cfg.Submitter(backend)
	if err != nil {
		return meta, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeFailedPrecondition), opRefresh, "submitter").
			With("batch_id", meta.ID)
	}
	var poll Poll
	if err = s.cfg.Breakers.Execute(ctx, meta.BackendID, func() error {
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
		return s.finalize(ctx, meta, poll.Lines)
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
	return meta, s.save(ctx, opRefresh, meta)
}

// save stores meta, naming op and the batch on failure.
func (s *Service) save(ctx context.Context, op string, meta batch.Meta) error {
	if err := s.cfg.Store.UpdateBatch(ctx, meta); err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInternal), op, "save").With("batch_id", meta.ID)
	}
	return nil
}

// finalize writes the output files and the terminal status. A batch that was
// cancelling ends cancelled and keeps the lines that finished.
func (s *Service) finalize(ctx context.Context, meta batch.Meta, lines []batch.LineResult) (batch.Meta, error) {
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
	updated, applied, err := s.cfg.Store.TryFinalizeBatch(ctx, meta.ID, patch)
	if err != nil {
		return meta, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInternal), opFinalize, "finalize").With("batch_id", meta.ID)
	}
	if !applied {
		// Another request finalised it first; return what it wrote.
		stored, err := s.cfg.Store.GetBatch(ctx, meta.ID)
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
	if err = s.cancelUpstream(ctx, sub, meta); err != nil {
		return meta, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), opCancel, "cancel").
			With("batch_id", meta.ID)
	}
	meta.Status = batch.StatusCancelling
	// The provider has accepted the cancel; a client hang-up must not lose the record.
	return meta, s.save(context.WithoutCancel(ctx), opCancel, meta)
}
