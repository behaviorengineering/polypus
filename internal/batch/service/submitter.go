package service

import (
	"context"

	"github.com/behaviorengineering/polypus/internal/batch"
)

// Job is one batch to submit: the downstream model, the OpenAI endpoint and its lines.
type Job struct {
	Downstream string
	Endpoint   string
	Lines      []batch.InputLine
}

// PollState is where an upstream batch stands.
type PollState int

// Upstream batch states.
const (
	PollRunning   PollState = iota // still processing.
	PollDone                       // finished; Poll.Lines holds every line.
	PollFailed                     // the batch failed as a whole, no output.
	PollExpired                    // the provider expired it, no output.
	PollCancelled                  // cancelled upstream with no output to keep.
)

// Poll is one look at an upstream batch.
type Poll struct {
	State PollState
	// Lines holds every finished line when State is PollDone.
	Lines []batch.LineResult
}

// Submitter runs batches on one provider.
type Submitter interface {
	// Validate rejects a job the provider cannot run, before anything is stored.
	Validate(job Job) error
	// Submit sends the job once and records the provider's reference (its batch id,
	// and anything else Poll needs) in meta.
	Submit(ctx context.Context, job Job, meta *batch.Meta) error
	// Poll reports the batch state, with its lines once it is done.
	Poll(ctx context.Context, meta batch.Meta) (Poll, error)
	// Cancel asks the provider to cancel; CodeUnimplemented when it cannot.
	Cancel(ctx context.Context, meta batch.Meta) error
}
