package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// openRouterBatchKeyEnv names the env var that holds the test key.
const openRouterBatchKeyEnv = "OPENROUTER_BATCH_TEST_KEY"

// fakeOpenRouter serves the batch create and retrieve calls with a settable status.
type fakeOpenRouter struct {
	mu      sync.Mutex
	status  string
	created int
}

func (f *fakeOpenRouter) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/batches":
		f.created++
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"batch_or_1","status":"validating"}`))
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/batches/batch_or_1":
		body := `{"id":"batch_or_1","status":"` + f.status + `","results":null}`
		if f.status == "completed" {
			body = `{"id":"batch_or_1","status":"completed","results":[
			 {"custom_id":"job-1","response":{"status_code":200,"body":{"object":"chat.completion","model":"anthropic/claude-haiku-5.5",
			   "choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}},"error":null}]}`
		}
		_, _ = w.Write([]byte(body))
	default:
		http.NotFound(w, r)
	}
}

func newOpenRouterBatchGateway(t *testing.T) (http.Handler, *fakeOpenRouter) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("POLYPUS_BATCH_DIR", filepath.Join(home, "state", "polypus", "batch"))
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	t.Setenv(openRouterBatchKeyEnv, "secret-key-at-least-twenty-ch")
	fake := &fakeOpenRouter{status: "in_progress"}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(srv.Close)
	reg, err := router.NewRegistry(config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Chat:     config.CapabilityBackend{Enabled: true, Default: "openrouter"},
		Batch:    config.CapabilityBackend{Enabled: true, Default: "openrouter"},
		Backends: map[string]config.BackendDef{
			"openrouter": {
				ID:           "openrouter",
				Remote:       true,
				BaseURL:      srv.URL + "/api/v1",
				Auth:         config.BackendAuth{BearerEnv: openRouterBatchKeyEnv},
				Capabilities: []config.Capability{config.CapChat, config.CapBatch},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return newTestGateway(t, config.ServeOptions{}, &fakeRouter{reg: reg}), fake
}

const openRouterBatchLine = `{"custom_id":"job-1","method":"POST","url":"/v1/chat/completions","body":{"model":"openrouter/anthropic/claude-haiku-5.5","messages":[{"role":"user","content":"hi"}]}}`

func TestOpenRouterBatchLifecycle(t *testing.T) {
	h, fake := newOpenRouterBatchGateway(t)
	rec := createClaudeBatch(t, h, openRouterBatchLine, batch.EndpointChatCompletions)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created batch.PublicBatch
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Status != batch.BatchStatusInProgress || fake.created != 1 {
		t.Fatalf("created=%+v submits=%d", created, fake.created)
	}
	if got := getBatch(t, h, created.ID); got.Status != batch.BatchStatusInProgress {
		t.Fatalf("still running: %+v", got)
	}

	fake.mu.Lock()
	fake.status = "completed"
	fake.mu.Unlock()
	done := getBatch(t, h, created.ID)
	if done.Status != batch.BatchStatusCompleted || done.RequestCounts.Completed != 1 {
		t.Fatalf("done: %+v", done)
	}
	out := fileContent(t, h, done.OutputFileID)
	if !strings.Contains(out, `"custom_id":"job-1"`) || !strings.Contains(out, "hello") ||
		!strings.Contains(out, `"model":"openrouter/anthropic/claude-haiku-5.5"`) {
		t.Fatalf("output: %s", out)
	}
}

func TestOpenRouterBatchFailedUpstream(t *testing.T) {
	h, fake := newOpenRouterBatchGateway(t)
	rec := createClaudeBatch(t, h, openRouterBatchLine, batch.EndpointChatCompletions)
	var created batch.PublicBatch
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.status = "failed"
	fake.mu.Unlock()
	if got := getBatch(t, h, created.ID); got.Status != batch.BatchStatusFailed || got.FailedAt == 0 {
		t.Fatalf("failed: %+v", got)
	}
}

func TestOpenRouterBatchRejectsInlineMedia(t *testing.T) {
	h, fake := newOpenRouterBatchGateway(t)
	line := `{"custom_id":"img-1","method":"POST","url":"/v1/chat/completions","body":{"model":"openrouter/anthropic/claude-haiku-5.5",` +
		`"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}}`
	rec := createClaudeBatch(t, h, line, batch.EndpointChatCompletions)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "base64") || fake.created != 0 {
		t.Fatalf("want a 400 before submitting: %d %s submits=%d", rec.Code, rec.Body.String(), fake.created)
	}
}

func TestOpenRouterBatchCancelUnsupported(t *testing.T) {
	h, _ := newOpenRouterBatchGateway(t)
	rec := createClaudeBatch(t, h, openRouterBatchLine, batch.EndpointChatCompletions)
	var created batch.PublicBatch
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/batches/"+created.ID+"/cancel", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body.String())
	}
}
