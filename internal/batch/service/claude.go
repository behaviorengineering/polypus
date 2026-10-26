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
	Create(ctx context.Context, backendID, model string, lines []batch.InputLine, timeout time.Duration) (string, error)
	Status(ctx context.Context, backendID, batchID string, timeout time.Duration) (router.BatchPoll, error)
	Results(ctx context.Context, backendID, batchID, publicModel string, timeout time.Duration) ([]batch.LineResult, error)
	Cancel(ctx context.Context, backendID, batchID string, timeout time.Duration) error
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
		return derrors.New(derrors.CodeInvalid, "batch.claude", "claude batches support /v1/chat/completions only").
			With("endpoint", job.Endpoint)
	}
	return nil
}

func (s claudeSubmitter) Submit(ctx context.Context, job Job, meta *batch.Meta) error {
	id, err := s.batches.Create(ctx, s.backend.ID, job.Downstream, job.Lines, remaining(ctx))
	if err != nil {
		return err
	}
	meta.UpstreamBatchID = id
	return nil
}

func (s claudeSubmitter) Poll(ctx context.Context, meta batch.Meta) (Poll, error) {
	st, err := s.batches.Status(ctx, s.backend.ID, meta.UpstreamBatchID, remaining(ctx))
	if err != nil {
		return Poll{}, err
	}
	if !st.Ended {
		return Poll{State: PollRunning}, nil
	}
	lines, err := s.batches.Results(ctx, s.backend.ID, meta.UpstreamBatchID, meta.PublicModel, remaining(ctx))
	if err != nil {
		return Poll{}, err
	}
	return Poll{State: PollDone, Lines: lines}, nil
}

func (s claudeSubmitter) Cancel(ctx context.Context, meta batch.Meta) error {
	return s.batches.Cancel(ctx, s.backend.ID, meta.UpstreamBatchID, remaining(ctx))
}

// remaining is the time left before ctx's deadline (the router passes it to Bifrost),
// or 0 when there is none.
func remaining(ctx context.Context) time.Duration {
	if d, ok := ctx.Deadline(); ok {
		return time.Until(d)
	}
	return 0
}
