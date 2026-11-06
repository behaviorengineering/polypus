package service

import (
	"context"
	"time"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// ClaudeBatches is the Anthropic Message Batches surface the Claude submitter needs;
// router.ClaudeBatches implements it.
type ClaudeBatches interface {
	Create(ctx context.Context, t router.BatchTarget, model string, lines []batch.InputLine) (string, error)
	Status(ctx context.Context, t router.BatchTarget, batchID string) (router.BatchPoll, error)
	Results(ctx context.Context, t router.BatchTarget, batchID, publicModel string) ([]batch.LineResult, error)
	Cancel(ctx context.Context, t router.BatchTarget, batchID string) error
}

const opClaude = "batch.claude"

// target is this submitter's backend with whatever time ctx has left.
func (s claudeSubmitter) target(ctx context.Context) router.BatchTarget {
	return router.BatchTarget{BackendID: s.backend.ID, Timeout: remaining(ctx)}
}

type claudeSubmitter struct {
	batches ClaudeBatches
	backend config.BackendDef
}

// NewClaudeSubmitter runs batches on an Anthropic backend through Message Batches.
// Panics if batches is nil.
func NewClaudeSubmitter(batches ClaudeBatches, backend config.BackendDef) Submitter {
	if batches == nil {
		panic("service.NewClaudeSubmitter: nil ClaudeBatches")
	}
	return claudeSubmitter{batches: batches, backend: backend}
}

func (s claudeSubmitter) Validate(job Job) error {
	if job.Endpoint != batch.EndpointChatCompletions {
		return derrors.New(derrors.CodeInvalid, opClaude, "claude batches support /v1/chat/completions only").
			With("endpoint", job.Endpoint)
	}
	return nil
}

func (s claudeSubmitter) Submit(ctx context.Context, job Job, meta *batch.Meta) error {
	id, err := s.batches.Create(ctx, s.target(ctx), job.Downstream, job.Lines)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), opClaude, "submit").With("batch_id", meta.ID)
	}
	meta.UpstreamBatchID = id
	return nil
}

func (s claudeSubmitter) Poll(ctx context.Context, meta batch.Meta) (Poll, error) {
	st, err := s.batches.Status(ctx, s.target(ctx), meta.UpstreamBatchID)
	if err != nil {
		return Poll{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), opClaude, "status").With("batch_id", meta.ID)
	}
	if !st.Ended {
		return Poll{State: PollRunning}, nil
	}
	lines, err := s.batches.Results(ctx, s.target(ctx), meta.UpstreamBatchID, meta.PublicModel)
	if err != nil {
		return Poll{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), opClaude, "results").With("batch_id", meta.ID)
	}
	return Poll{State: PollDone, Lines: lines}, nil
}

func (s claudeSubmitter) Cancel(ctx context.Context, meta batch.Meta) error {
	if err := s.batches.Cancel(ctx, s.target(ctx), meta.UpstreamBatchID); err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), opClaude, "cancel").With("batch_id", meta.ID)
	}
	return nil
}

// remaining is the time left before ctx's deadline (the router passes it to Bifrost),
// or 0 when there is none.
func remaining(ctx context.Context) time.Duration {
	if d, ok := ctx.Deadline(); ok {
		return time.Until(d)
	}
	return 0
}
