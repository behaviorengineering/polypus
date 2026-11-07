package batches

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	"github.com/behaviorengineering/polypus/internal/clients/openrouter"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
	"github.com/behaviorengineering/polypus/internal/statefile"
)

// fakeSubmitter scripts one provider.
type fakeSubmitter struct {
	submitErr error
	poll      Poll
	// onSubmit runs inside a successful Submit (a test cancels the caller's ctx there).
	onSubmit func()
	// breakSave gives the batch an id the store refuses, so the save after a
	// successful submit fails.
	breakSave bool
	cancelErr error
	cancels   int
	// onCancel runs inside Cancel once the provider has accepted it.
	onCancel func()
	// cancelCtxErr and cancelHasDeadline record the ctx of the last Cancel.
	cancelCtxErr      error
	cancelHasDeadline bool
}

func (f *fakeSubmitter) Validate(Job) error { return nil }

func (f *fakeSubmitter) Submit(_ context.Context, _ Job, meta *batch.Meta) error {
	if f.submitErr != nil {
		return f.submitErr
	}
	meta.UpstreamBatchID = "up_1"
	if f.breakSave {
		meta.ID = "../outside"
	}
	if f.onSubmit != nil {
		f.onSubmit()
	}
	return nil
}

func (f *fakeSubmitter) Poll(context.Context, batch.Meta) (Poll, error) { return f.poll, nil }

func (f *fakeSubmitter) Cancel(ctx context.Context, _ batch.Meta) error {
	f.cancels++
	f.cancelCtxErr = ctx.Err()
	_, f.cancelHasDeadline = ctx.Deadline()
	if f.cancelErr == nil && f.onCancel != nil {
		f.onCancel()
	}
	return f.cancelErr
}

func newTestService(t *testing.T, sub *fakeSubmitter, now *time.Time) (*Service, *batch.DiskStore) {
	t.Helper()
	svc, store, _ := newTestServiceWithLogs(t, sub, now)
	return svc, store
}

// newTestServiceWithLogs is newTestService with the service's log lines kept in a
// buffer, one text record per line.
func newTestServiceWithLogs(t *testing.T, sub Submitter, now *time.Time) (*Service, *batch.DiskStore, *bytes.Buffer) {
	t.Helper()
	clock := func() time.Time { return *now }
	store, err := batch.NewDiskStore(t.TempDir(), clock)
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	svc, err := Config{
		Store:     store,
		Breakers:  upstream.NewBoard(),
		Now:       clock,
		Submitter: func(config.BackendDef) (Submitter, error) { return sub, nil },
		Logger:    slog.New(slog.NewTextHandler(logs, nil)),
	}.CreateService()
	if err != nil {
		t.Fatal(err)
	}
	return svc, store, logs
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
	stored, gerr := store.GetBatch(t.Context(), meta.ID)
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
	if meta, err = svc.Cancel(context.Background(), batchBackend, meta); err != nil || meta.Status != batch.StatusCancelling || sub.cancels != 1 {
		t.Fatalf("cancel: %+v %v", meta, err)
	}
	sub.poll = Poll{State: PollDone, Lines: []batch.LineResult{{CustomID: "a", Body: []byte(`{}`)}}}
	if meta, err = svc.Refresh(context.Background(), batchBackend, meta); err != nil || meta.Status != batch.StatusCancelled || meta.RequestCounts.Completed != 1 {
		t.Fatalf("a cancelling batch ends cancelled with the finished lines: %+v %v", meta, err)
	}
}

// TestCancelSavesCancellingAfterTheClientLeaves checks a client that hangs up after
// the provider accepted the cancel still leaves the record saying cancelling, even
// when the save has to wait for another holder of the store's lock.
func TestCancelSavesCancellingAfterTheClientLeaves(t *testing.T) {
	now := time.Unix(1_000, 0)
	clock := func() time.Time { return now }
	root := t.TempDir()
	store, err := batch.NewDiskStore(root, clock)
	if err != nil {
		t.Fatal(err)
	}
	sub := &fakeSubmitter{poll: Poll{State: PollRunning}}
	svc, err := Config{
		Store:     store,
		Breakers:  upstream.NewBoard(),
		Now:       clock,
		Submitter: func(config.BackendDef) (Submitter, error) { return sub, nil },
		Logger:    slog.New(slog.DiscardHandler),
	}.CreateService()
	if err != nil {
		t.Fatal(err)
	}
	meta, err := svc.Create(context.Background(), createRequest())
	if err != nil {
		t.Fatal(err)
	}

	// Another gateway holds the store's lock while the client hangs up.
	held, release := make(chan struct{}), make(chan struct{})
	lockCtx, stopLock := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopLock()
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- statefile.WithFileLock(lockCtx, filepath.Join(root, "store"), func() error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	ctx, hangUp := context.WithCancel(context.Background())
	defer hangUp()
	sub.onCancel = func() {
		hangUp()
		time.AfterFunc(100*time.Millisecond, func() { close(release) })
	}
	got, err := svc.Cancel(ctx, batchBackend, meta)
	if err != nil || got.Status != batch.StatusCancelling {
		t.Fatalf("cancel: %+v %v", got, err)
	}
	if err := <-lockDone; err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetBatch(t.Context(), meta.ID)
	if err != nil || stored.Status != batch.StatusCancelling {
		t.Fatalf("the record must say cancelling: %+v %v", stored, err)
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

// brokenSaveSubmitter fails the submit and breaks the batch id, so saving the failed
// status fails too.
type brokenSaveSubmitter struct{ fakeSubmitter }

func (b *brokenSaveSubmitter) Submit(_ context.Context, _ Job, meta *batch.Meta) error {
	meta.ID = "../outside"
	return derrors.New(derrors.CodeRateLimited, "p", "provider said no")
}

func TestCreateKeepsTheSubmitErrorWhenTheSaveFails(t *testing.T) {
	now := time.Unix(1_000, 0)
	svc, _, _ := newTestServiceWithLogs(t, &brokenSaveSubmitter{}, &now)
	_, err := svc.Create(context.Background(), createRequest())
	if derrors.CodeOf(err) != derrors.CodeRateLimited || !strings.Contains(err.Error(), "provider said no") {
		t.Fatalf("the provider's rejection must survive a failed save: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid id") {
		t.Fatalf("the save failure must be kept too: %v", err)
	}
}

func TestRefreshRejectsAPollWithNoState(t *testing.T) {
	now := time.Unix(1_000, 0)
	sub := &fakeSubmitter{poll: Poll{State: PollRunning}}
	svc, _ := newTestService(t, sub, &now)
	meta, err := svc.Create(context.Background(), createRequest())
	if err != nil {
		t.Fatal(err)
	}
	sub.poll = Poll{}
	if _, err := svc.Refresh(context.Background(), batchBackend, meta); derrors.CodeOf(err) != derrors.CodeInternal {
		t.Fatalf("a zero Poll must fail, not read as running: %v", err)
	}
}

func TestRefreshAndCancelStartSpans(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})
	now := time.Unix(1_000, 0)
	sub := &fakeSubmitter{poll: Poll{State: PollRunning}}
	svc, _ := newTestService(t, sub, &now)
	meta, err := svc.Create(context.Background(), createRequest())
	if err != nil {
		t.Fatal(err)
	}
	if meta, err = svc.Refresh(context.Background(), batchBackend, meta); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Cancel(context.Background(), batchBackend, meta); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, sp := range exporter.GetSpans() {
		names[sp.Name] = true
	}
	if !names["polypus.batch.refresh"] || !names["polypus.batch.cancel"] {
		t.Fatalf("spans = %v", names)
	}
}

func TestCloudflareUnknownChatResultFailsTheLine(t *testing.T) {
	lines := cloudflareLines(batch.EndpointChatCompletions, "b/m", 1, []cloudflare.BatchResponseItem{
		{ExternalReference: "a", Success: true, Result: []byte(`{"unexpected":true}`)},
		{ExternalReference: "b", Success: true, Result: []byte(`{"response":"hi"}`)},
	})
	if lines[0].ErrCode != batch.LineErrSynthesis || len(lines[0].Body) != 0 {
		t.Fatalf("an unknown result must fail its line: %+v", lines[0])
	}
	if lines[1].ErrCode != "" || !strings.Contains(string(lines[1].Body), `"content":"hi"`) {
		t.Fatalf("a response result becomes a completion: %+v", lines[1])
	}
}

// expiredBatch stores a submitted batch whose completion window has passed by now.
func expiredBatch(t *testing.T, store *batch.DiskStore, now time.Time, upstreamID string) batch.Meta {
	t.Helper()
	meta := batch.Meta{
		Status: batch.StatusInProgress, BackendID: batchBackend.ID, Endpoint: batch.EndpointChatCompletions,
		UpstreamBatchID: upstreamID, ExpiresAt: now.Add(-time.Hour).Unix(),
	}
	if err := store.SaveBatch(t.Context(), &meta); err != nil {
		t.Fatal(err)
	}
	return meta
}

func TestRefreshExpiredCancelsUpstream(t *testing.T) {
	now := time.Unix(100_000, 0)
	sub := &fakeSubmitter{poll: Poll{State: PollRunning}}
	svc, store, logs := newTestServiceWithLogs(t, sub, &now)
	meta, err := svc.Refresh(t.Context(), batchBackend, expiredBatch(t, store, now, "up_1"))
	if err != nil || meta.Status != batch.StatusExpired {
		t.Fatalf("an expired batch ends expired: %+v %v", meta, err)
	}
	if sub.cancels != 1 {
		t.Fatalf("want one upstream cancel, got %d", sub.cancels)
	}
	if logs.Len() != 0 {
		t.Fatalf("a successful cancel logs nothing: %s", logs)
	}
}

func TestRefreshExpiredCancelFailureStillExpires(t *testing.T) {
	now := time.Unix(100_000, 0)
	sub := &fakeSubmitter{cancelErr: derrors.New(derrors.CodeUnavailable, "p", "provider down")}
	svc, store, logs := newTestServiceWithLogs(t, sub, &now)
	meta, err := svc.Refresh(t.Context(), batchBackend, expiredBatch(t, store, now, "up_1"))
	if err != nil || meta.Status != batch.StatusExpired {
		t.Fatalf("a failed cancel must not block the expiry: %+v %v", meta, err)
	}
	stored, err := store.GetBatch(t.Context(), meta.ID)
	if err != nil || stored.Status != batch.StatusExpired {
		t.Fatalf("the expiry is saved: %+v %v", stored, err)
	}
	if out := logs.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "cancel of expired batch failed") ||
		!strings.Contains(out, "upstream_batch_id=up_1") {
		t.Fatalf("want a warning naming the upstream batch, got %q", out)
	}
}

func TestRefreshExpiredUnimplementedCancelIsQuiet(t *testing.T) {
	now := time.Unix(100_000, 0)
	sub := &fakeSubmitter{cancelErr: derrors.New(derrors.CodeUnimplemented, "p", "cannot cancel")}
	svc, store, logs := newTestServiceWithLogs(t, sub, &now)
	meta, err := svc.Refresh(t.Context(), batchBackend, expiredBatch(t, store, now, "up_1"))
	if err != nil || meta.Status != batch.StatusExpired || sub.cancels != 1 {
		t.Fatalf("expired %+v, err %v, cancels %d", meta, err, sub.cancels)
	}
	if logs.Len() != 0 {
		t.Fatalf("a provider that cannot cancel is skipped quietly: %s", logs)
	}
}

func TestRefreshExpiredWithoutUpstreamIDSkipsCancel(t *testing.T) {
	now := time.Unix(100_000, 0)
	sub := &fakeSubmitter{}
	svc, store, _ := newTestServiceWithLogs(t, sub, &now)
	meta, err := svc.Refresh(t.Context(), batchBackend, expiredBatch(t, store, now, ""))
	if err != nil || meta.Status != batch.StatusExpired {
		t.Fatalf("expired %+v, err %v", meta, err)
	}
	if sub.cancels != 0 {
		t.Fatalf("a batch with no upstream id has nothing to cancel, got %d cancels", sub.cancels)
	}
}

func TestUnimplementedCancelDoesNotTripTheBreaker(t *testing.T) {
	now := time.Unix(100_000, 0)
	sub := &fakeSubmitter{cancelErr: derrors.New(derrors.CodeUnimplemented, "p", "cannot cancel")}
	svc, store, _ := newTestServiceWithLogs(t, sub, &now)
	for range 10 {
		if _, err := svc.Refresh(t.Context(), batchBackend, expiredBatch(t, store, now, "up_1")); err != nil {
			t.Fatal(err)
		}
	}
	if sub.cancels != 10 {
		t.Fatalf("every expiry asks once; a provider that cannot cancel is not a failing upstream (breaker open?): %d", sub.cancels)
	}
}

func TestCreateSaveFailureCancelsUpstream(t *testing.T) {
	now := time.Unix(1_000, 0)
	sub := &fakeSubmitter{breakSave: true}
	svc, _, _ := newTestServiceWithLogs(t, sub, &now)
	_, err := svc.Create(t.Context(), createRequest())
	if err == nil || !strings.Contains(err.Error(), "upstream cancelled") {
		t.Fatalf("a failed save after a submit must fail the create: %v", err)
	}
	if sub.cancels != 1 {
		t.Fatalf("the unsaved upstream batch must be cancelled once, got %d", sub.cancels)
	}
}

func TestCreateSaveFailureCancelUsesOwnDeadline(t *testing.T) {
	now := time.Unix(1_000, 0)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// The caller goes away while the provider is accepting the batch.
	sub := &fakeSubmitter{breakSave: true, onSubmit: cancel}
	svc, _, _ := newTestServiceWithLogs(t, sub, &now)
	if _, err := svc.Create(ctx, createRequest()); err == nil {
		t.Fatal("want the save error")
	}
	if sub.cancels != 1 || sub.cancelCtxErr != nil || !sub.cancelHasDeadline {
		t.Fatalf("the cancel needs a live ctx with its own deadline: cancels %d, ctx err %v, deadline %v",
			sub.cancels, sub.cancelCtxErr, sub.cancelHasDeadline)
	}
}

func TestCreateSaveFailureCancelFailsLogsUpstreamID(t *testing.T) {
	now := time.Unix(1_000, 0)
	sub := &fakeSubmitter{breakSave: true, cancelErr: derrors.New(derrors.CodeUnimplemented, "p", "cannot cancel")}
	svc, _, logs := newTestServiceWithLogs(t, sub, &now)
	_, err := svc.Create(t.Context(), createRequest())
	if err == nil || !strings.Contains(err.Error(), "upstream not cancelled") {
		t.Fatalf("the error must say the upstream batch is still there: %v", err)
	}
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("the save failure's code leads: %v", err)
	}
	if out := logs.String(); !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "upstream_batch_id=up_1") {
		t.Fatalf("want an error line carrying the upstream id, got %q", out)
	}
}
