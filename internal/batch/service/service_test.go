package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/clients/openrouter"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
)

// fakeSubmitter scripts one provider.
type fakeSubmitter struct {
	submitErr error
	poll      Poll
	cancelled bool
}

func (f *fakeSubmitter) Validate(Job) error { return nil }

func (f *fakeSubmitter) Submit(_ context.Context, _ Job, meta *batch.Meta) error {
	if f.submitErr != nil {
		return f.submitErr
	}
	meta.UpstreamBatchID = "up_1"
	return nil
}

func (f *fakeSubmitter) Poll(context.Context, batch.Meta) (Poll, error) { return f.poll, nil }

func (f *fakeSubmitter) Cancel(context.Context, batch.Meta) error {
	f.cancelled = true
	return nil
}

func newTestService(t *testing.T, sub *fakeSubmitter, now *time.Time) (*Service, *batch.DiskStore) {
	t.Helper()
	clock := func() time.Time { return *now }
	store, err := batch.NewDiskStore(t.TempDir(), clock)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := Config{
		Store:     store,
		Breakers:  upstream.NewBoard(),
		Now:       clock,
		Submitter: func(config.BackendDef) (Submitter, error) { return sub, nil },
	}.CreateService()
	if err != nil {
		t.Fatal(err)
	}
	return svc, store
}

var batchBackend = config.BackendDef{ID: "b", Capabilities: []config.Capability{config.CapBatch}}

func createRequest() CreateRequest {
	return CreateRequest{
		Backend: batchBackend, PublicModel: "b/m", InputFileID: "file_1", CompletionWindow: "24h",
		Job: Job{Downstream: "m", Endpoint: batch.EndpointChatCompletions, Lines: []batch.InputLine{{CustomID: "a"}, {CustomID: "b"}}},
	}
}

func TestCreateRecordsAFailedSubmit(t *testing.T) {
	now := time.Unix(1_000, 0)
	svc, store := newTestService(t, &fakeSubmitter{submitErr: derrors.New(derrors.CodeRateLimited, "p", "slow down")}, &now)
	meta, err := svc.Create(context.Background(), createRequest())
	if derrors.CodeOf(err) != derrors.CodeRateLimited {
		t.Fatalf("the provider's code must survive: %v", err)
	}
	stored, gerr := store.GetBatch(meta.ID)
	if gerr != nil || stored.Status != batch.StatusFailed || stored.FailedAt == 0 {
		t.Fatalf("a failed submit stays stored as failed: %+v %v", stored, gerr)
	}
}

func TestCreateThenFinishWritesOutput(t *testing.T) {
	now := time.Unix(1_000, 0)
	sub := &fakeSubmitter{poll: Poll{State: PollRunning}}
	svc, _ := newTestService(t, sub, &now)
	meta, err := svc.Create(context.Background(), createRequest())
	if err != nil || meta.Status != batch.StatusInProgress || meta.UpstreamBatchID != "up_1" {
		t.Fatalf("create: %+v %v", meta, err)
	}
	if meta, err = svc.Refresh(context.Background(), batchBackend, meta); err != nil || meta.Status != batch.StatusInProgress {
		t.Fatalf("running: %+v %v", meta, err)
	}
	sub.poll = Poll{State: PollDone, Lines: []batch.LineResult{
		{CustomID: "a", Body: []byte(`{"object":"chat.completion"}`)},
		{CustomID: "b", ErrCode: "errored", ErrMessage: "boom"},
	}}
	meta, err = svc.Refresh(context.Background(), batchBackend, meta)
	if err != nil || meta.Status != batch.StatusCompleted || meta.RequestCounts.Completed != 1 || meta.RequestCounts.Failed != 1 {
		t.Fatalf("done: %+v %v", meta, err)
	}
}

func TestRefreshExpiresAPastWindow(t *testing.T) {
	now := time.Unix(1_000, 0)
	svc, _ := newTestService(t, &fakeSubmitter{poll: Poll{State: PollRunning}}, &now)
	meta, err := svc.Create(context.Background(), createRequest())
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(48 * time.Hour)
	if meta, err = svc.Refresh(context.Background(), batchBackend, meta); err != nil || meta.Status != batch.StatusExpired {
		t.Fatalf("past its window: %+v %v", meta, err)
	}
}

func TestCancelledBatchFinishesCancelledWithItsLines(t *testing.T) {
	now := time.Unix(1_000, 0)
	sub := &fakeSubmitter{poll: Poll{State: PollRunning}}
	svc, _ := newTestService(t, sub, &now)
	meta, err := svc.Create(context.Background(), createRequest())
	if err != nil {
		t.Fatal(err)
	}
	if meta, err = svc.Cancel(context.Background(), batchBackend, meta); err != nil || meta.Status != batch.StatusCancelling || !sub.cancelled {
		t.Fatalf("cancel: %+v %v", meta, err)
	}
	sub.poll = Poll{State: PollDone, Lines: []batch.LineResult{{CustomID: "a", Body: []byte(`{}`)}}}
	if meta, err = svc.Refresh(context.Background(), batchBackend, meta); err != nil || meta.Status != batch.StatusCancelled || meta.RequestCounts.Completed != 1 {
		t.Fatalf("a cancelling batch ends cancelled with the finished lines: %+v %v", meta, err)
	}
}

func TestCreateServiceRequiresEveryDependency(t *testing.T) {
	if _, err := (Config{}).CreateService(); err == nil {
		t.Fatal("an empty config must be rejected")
	}
}

func TestOpenRouterRejectsInlineMedia(t *testing.T) {
	sub := NewOpenRouterSubmitter(func() (*openrouter.Client, error) { return nil, errors.New("unused") })
	job := Job{Lines: []batch.InputLine{{CustomID: "img", Body: []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}`)}}}
	if err := sub.Validate(job); derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("a base64 image must be refused: %v", err)
	}
	job.Lines[0].Body = []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	if err := sub.Validate(job); err != nil {
		t.Fatalf("plain text passes: %v", err)
	}
}
