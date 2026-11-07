package batches

import (
	"context"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// ClaudeBatches is the Anthropic Message Batches surface the Claude submitter needs;
// router.ClaudeBatches implements it.
type ClaudeBatches interface {
	Create(ctx context.Context, backendID, model string, lines []batch.InputLine) (string, error)
	Status(ctx context.Context, backendID, batchID string) (router.BatchPoll, error)
	Results(ctx context.Context, backendID, batchID, publicModel string) ([]batch.LineResult, error)
	Cancel(ctx context.Context, backendID, batchID string) error
}

const opClaude = "batches.claude"

type claudeSubmitter struct {
	batches ClaudeBatches
	backend config.BackendDef
}

// NewClaudeSubmitter runs batches on an Anthropic backend through Message Batches.
// Panics if batches is nil.
func NewClaudeSubmitter(batches ClaudeBatches, backend config.BackendDef) Submitter {
	if batches == nil {
		panic("batches.NewClaudeSubmitter: nil ClaudeBatches")
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
	id, err := s.batches.Create(ctx, s.backend.ID, job.Downstream, job.Lines)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), opClaude, "submit").With("batch_id", meta.ID)
	}
	meta.UpstreamBatchID = id
	return nil
}

func (s claudeSubmitter) Poll(ctx context.Context, meta batch.Meta) (Poll, error) {
	st, err := s.batches.Status(ctx, s.backend.ID, meta.UpstreamBatchID)
	if err != nil {
		return Poll{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), opClaude, "status").With("batch_id", meta.ID)
	}
	if !st.Ended {
		return Poll{State: PollRunning}, nil
	}
	lines, err := s.batches.Results(ctx, s.backend.ID, meta.UpstreamBatchID, meta.PublicModel)
	if err != nil {
		return Poll{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), opClaude, "results").With("batch_id", meta.ID)
	}
	return Poll{State: PollDone, Lines: lines}, nil
}

func (s claudeSubmitter) Cancel(ctx context.Context, meta batch.Meta) error {
	if err := s.batches.Cancel(ctx, s.backend.ID, meta.UpstreamBatchID); err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), opClaude, "cancel").With("batch_id", meta.ID)
	}
	return nil
}
