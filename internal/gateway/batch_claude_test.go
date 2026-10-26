package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// claudeBatchKeyEnv names the env var that holds the test key.
const claudeBatchKeyEnv = "CLAUDE_BATCH_TEST_KEY"

// claudeBatchRouter scripts the Claude Message Batches calls over a fake registry.
type claudeBatchRouter struct {
	*fakeRouter
	createdLines []batch.InputLine
	createdModel string
	ended        bool
	cancelled    bool
	results      []router.BatchLineResult
}

func (r *claudeBatchRouter) ClaudeBatchCreate(_ context.Context, _, model string, lines []batch.InputLine, _ time.Duration) (string, error) {
	r.createdModel = model
	r.createdLines = lines
	return "msgbatch_1", nil
}

func (r *claudeBatchRouter) ClaudeBatchStatus(_ context.Context, _, batchID string, _ time.Duration) (router.BatchPoll, error) {
	if batchID != "msgbatch_1" {
		return router.BatchPoll{}, context.Canceled
	}
	return router.BatchPoll{Ended: r.ended, Cancelling: r.cancelled && !r.ended}, nil
}

func (r *claudeBatchRouter) ClaudeBatchResults(_ context.Context, _, _, _ string, _ time.Duration) ([]router.BatchLineResult, error) {
	return r.results, nil
}

func (r *claudeBatchRouter) ClaudeBatchCancel(context.Context, string, string, time.Duration) error {
	r.cancelled = true
	return nil
}

func newClaudeBatchGateway(t *testing.T) (http.Handler, *claudeBatchRouter) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("POLYPUS_BATCH_DIR", filepath.Join(home, "state", "polypus", "batch"))
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	t.Setenv(claudeBatchKeyEnv, "secret-key-at-least-twenty-ch")
	reg, err := router.NewRegistry(config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Chat:     config.CapabilityBackend{Enabled: true, Default: "claude_team"},
		Batch:    config.CapabilityBackend{Enabled: true, Default: "claude_team"},
		Backends: map[string]config.BackendDef{
			"claude_team": {
				ID:           "claude_team",
				Remote:       true,
				Extension:    config.ExtensionAnthropic,
				Auth:         config.BackendAuth{BearerEnv: claudeBatchKeyEnv},
				Capabilities: []config.Capability{config.CapChat, config.CapBatch},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fr := &claudeBatchRouter{fakeRouter: &fakeRouter{reg: reg}}
	return newTestGateway(t, config.ServeOptions{}, fr), fr
}

const claudeBatchLine = `{"custom_id":"job-1","method":"POST","url":"/v1/chat/completions","body":{"model":"claude_team/claude-sonnet-5-5","messages":[{"role":"user","content":"hi"}]}}`

func createClaudeBatch(t *testing.T, h http.Handler, line, endpoint string) *httptest.ResponseRecorder {
	t.Helper()
	fileID := uploadBatchFile(t, h, line)
	body := `{"input_file_id":"` + fileID + `","endpoint":"` + endpoint + `","completion_window":"24h"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/batches", strings.NewReader(body)))
	return rec
}

func getBatch(t *testing.T, h http.Handler, id string) batch.PublicBatch {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/batches/"+id, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get batch: %d %s", rec.Code, rec.Body.String())
	}
	var b batch.PublicBatch
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func fileContent(t *testing.T, h http.Handler, id string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/files/"+id+"/content", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("file content: %d %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestClaudeBatchLifecycle(t *testing.T) {
	h, fr := newClaudeBatchGateway(t)
	rec := createClaudeBatch(t, h, claudeBatchLine, batch.EndpointChatCompletions)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created batch.PublicBatch
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Status != batch.BatchStatusInProgress || fr.createdModel != "claude-sonnet-5-5" || len(fr.createdLines) != 1 {
		t.Fatalf("created=%+v model=%q lines=%d", created, fr.createdModel, len(fr.createdLines))
	}

	if got := getBatch(t, h, created.ID); got.Status != batch.BatchStatusInProgress {
		t.Fatalf("still running: %+v", got)
	}

	fr.ended = true
	fr.results = []router.BatchLineResult{
		{CustomID: "job-1", Body: json.RawMessage(`{"object":"chat.completion","model":"claude_team/claude-sonnet-5-5","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`)},
		{CustomID: "job-2", ErrCode: "errored", ErrMessage: "invalid_request_error"},
	}
	done := getBatch(t, h, created.ID)
	if done.Status != batch.BatchStatusCompleted || done.RequestCounts.Completed != 1 || done.RequestCounts.Failed != 1 {
		t.Fatalf("done: %+v", done)
	}
	if out := fileContent(t, h, done.OutputFileID); !strings.Contains(out, `"custom_id":"job-1"`) || !strings.Contains(out, "hello") {
		t.Fatalf("output: %s", out)
	}
	if errs := fileContent(t, h, done.ErrorFileID); !strings.Contains(errs, `"custom_id":"job-2"`) || !strings.Contains(errs, "errored") {
		t.Fatalf("errors: %s", errs)
	}
}

func TestClaudeBatchCancelKeepsFinishedLines(t *testing.T) {
	h, fr := newClaudeBatchGateway(t)
	rec := createClaudeBatch(t, h, claudeBatchLine, batch.EndpointChatCompletions)
	var created batch.PublicBatch
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/batches/"+created.ID+"/cancel", nil))
	if rec.Code != http.StatusOK || !fr.cancelled {
		t.Fatalf("cancel: %d %s cancelled=%v", rec.Code, rec.Body.String(), fr.cancelled)
	}
	if got := getBatch(t, h, created.ID); got.Status != batch.BatchStatusCancelling {
		t.Fatalf("cancelling: %+v", got)
	}

	fr.ended = true
	fr.results = []router.BatchLineResult{
		{CustomID: "job-1", Body: json.RawMessage(`{"object":"chat.completion","choices":[]}`)},
	}
	done := getBatch(t, h, created.ID)
	if done.Status != batch.BatchStatusCancelled || done.CancelledAt == 0 || done.OutputFileID == "" {
		t.Fatalf("cancelled: %+v", done)
	}
}

func TestClaudeBatchRejectsEmbeddings(t *testing.T) {
	h, _ := newClaudeBatchGateway(t)
	line := `{"custom_id":"e-1","method":"POST","url":"/v1/embeddings","body":{"model":"claude_team/claude-sonnet-5-5","input":"hi"}}`
	rec := createClaudeBatch(t, h, line, batch.EndpointEmbeddings)
	if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "chat/completions only") {
		t.Fatalf("want a chat-only rejection: %d %s", rec.Code, rec.Body.String())
	}
}

func TestBuildBatchLineOutputCounts(t *testing.T) {
	ok, errs, completed, failed := buildBatchLineOutput([]router.BatchLineResult{
		{CustomID: "a", Body: json.RawMessage(`{"x":1}`)},
		{CustomID: "b", ErrCode: "expired", ErrMessage: "expired"},
		{CustomID: "c", Body: json.RawMessage(`{"x":2}`)},
	})
	if completed != 2 || failed != 1 || strings.Count(string(ok), "\n") != 2 || strings.Count(string(errs), "\n") != 1 {
		t.Fatalf("completed=%d failed=%d ok=%q errs=%q", completed, failed, ok, errs)
	}
}
