package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestSubmitBatchBody(t *testing.T) {
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/batches" || r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "bad request line", http.StatusBadRequest)
			return
		}
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"batch_1","object":"batch","status":"validating"}`))
	}))
	t.Cleanup(srv.Close)

	c, err := newClient(srv.URL+"/api/v1", "secret")
	if err != nil {
		t.Fatal(err)
	}
	id, err := c.SubmitBatch(testCtx(t), "anthropic/claude-haiku-5.5", batch.EndpointChatCompletions, []batch.InputLine{{
		CustomID: "job-1",
		Body:     json.RawMessage(`{"model":"openrouter/anthropic/claude-haiku-5.5","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
	}})
	if err != nil || id != "batch_1" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if bytes.Index(got, []byte(`"requests"`)) < bytes.Index(got, []byte(`"completion_window"`)) {
		t.Fatalf("requests must be the last field: %s", got)
	}
	var sent struct {
		Endpoint         string `json:"endpoint"`
		Model            string `json:"model"`
		CompletionWindow string `json:"completion_window"`
		Requests         []struct {
			CustomID string         `json:"custom_id"`
			Body     map[string]any `json:"body"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(got, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Endpoint != batch.EndpointChatCompletions || sent.Model != "anthropic/claude-haiku-5.5" || sent.CompletionWindow != "24h" {
		t.Fatalf("batch fields: %s", got)
	}
	if len(sent.Requests) != 1 || sent.Requests[0].CustomID != "job-1" {
		t.Fatalf("requests: %s", got)
	}
	if _, ok := sent.Requests[0].Body["model"]; ok {
		t.Fatal("item bodies must drop model; the batch sets it")
	}
	if _, ok := sent.Requests[0].Body["stream"]; ok {
		t.Fatal("item bodies must drop stream")
	}
}

func TestGetBatchResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/batches/batch_1" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"id":"batch_1","status":"completed","request_counts":{"total":2,"completed":1,"failed":1},
			"results":[
			 {"custom_id":"job-1","response":{"status_code":200,"body":{"object":"chat.completion","choices":[]}},"error":null},
			 {"custom_id":"job-2","response":null,"error":{"type":"invalid_request_error","message":"max_tokens too large"}}]}`))
	}))
	t.Cleanup(srv.Close)

	c, err := newClient(srv.URL+"/api/v1", "secret")
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.GetBatch(testCtx(t), "batch_1")
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != StatusCompleted || len(b.Results) != 2 || b.Results[0].Response.StatusCode != 200 || b.Results[1].Error.Type != "invalid_request_error" {
		t.Fatalf("batch: %+v", b)
	}
}

func TestBatchErrorCodes(t *testing.T) {
	for status, want := range map[int]derrors.Code{
		http.StatusPaymentRequired:     derrors.CodeInvalid,
		http.StatusTooManyRequests:     derrors.CodeRateLimited,
		http.StatusUnauthorized:        derrors.CodeUnauthorized,
		http.StatusGone:                derrors.CodeNotFound,
		http.StatusInternalServerError: derrors.CodeUnavailable,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":1,"message":"estimated cost exceeds balance"}}`))
		}))
		c, err := newClient(srv.URL, "secret")
		if err != nil {
			t.Fatal(err)
		}
		// A retrieve retries 429 and 5xx; a millisecond backoff keeps that out of real time.
		c.policies = outbound.NewPoliciesWith(outbound.Backoff{Min: time.Millisecond, Max: time.Millisecond})
		_, err = c.GetBatch(testCtx(t), "x")
		srv.Close()
		var de *derrors.Error
		if !errors.As(err, &de) || de.Code() != want || !strings.Contains(err.Error(), "estimated cost exceeds balance") {
			t.Fatalf("status %d: err=%v", status, err)
		}
	}
}

func TestAPIBase(t *testing.T) {
	if apiBase("") != defaultAPIBase || apiBase("https://openrouter.ai/api/v1/") != "https://openrouter.ai/api/v1" {
		t.Fatal("apiBase")
	}
}

func TestClientsGetRejectsOtherBackends(t *testing.T) {
	t.Setenv("OR_TEST_KEY", "secret")
	_, err := (&Clients{}).Get(config.BackendDef{
		ID: "cf_local", BaseURL: "https://api.cloudflare.com/client/v4/accounts/a/ai/v1",
		Auth: config.BackendAuth{BearerEnv: "OR_TEST_KEY"},
	})
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("a non-OpenRouter backend must be refused, got %v", err)
	}
	if _, err := (&Clients{}).Get(config.BackendDef{ID: "openrouter", Auth: config.BackendAuth{BearerEnv: "OR_TEST_KEY"}}); err != nil {
		t.Fatalf("openrouter backend: %v", err)
	}
}

func TestDoKeepsTheOutboundCode(t *testing.T) {
	c, err := newClient("http://127.0.0.1:1", "secret")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.GetBatch(context.Background(), "x")
	if derrors.CodeOf(err) != derrors.CodeFailedPrecondition {
		t.Fatalf("a call without a deadline keeps the outbound code, got %v", err)
	}
}

func TestNilClientsGetIsAnError(t *testing.T) {
	var cs *Clients
	_, err := cs.Get(config.BackendDef{ID: "openrouter"})
	if derrors.CodeOf(err) != derrors.CodeInternal {
		t.Fatalf("err = %v, want an internal error instead of a panic", err)
	}
}

func TestGetBatchRequiresAnID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("an empty id must not reach the list endpoint")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)
	c, err := newClient(srv.URL+"/api/v1", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetBatch(testCtx(t), "  "); derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("err = %v, want invalid", err)
	}
}

// trackedBody records whether the caller closed the response body.
type trackedBody struct {
	io.Reader
	closed atomic.Bool
}

func (b *trackedBody) Close() error {
	b.closed.Store(true)
	return nil
}

type busyTransport struct{ body *trackedBody }

func (b busyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}, Body: b.body, Request: r}, nil
}

// TestGetBatchClosesTheResponseADeadlineInterrupts: a retrieve retries a 503, the
// deadline ends the backoff, and outbound.Do hands back the last answer with the
// error. The client must close it, or the connection leaks.
func TestGetBatchClosesTheResponseADeadlineInterrupts(t *testing.T) {
	body := &trackedBody{Reader: strings.NewReader("busy")}
	c, err := newClient("https://openrouter.invalid/api/v1", "secret")
	if err != nil {
		t.Fatal(err)
	}
	c.httpClient = &http.Client{Transport: busyTransport{body: body}}
	c.policies = outbound.NewPolicies()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := c.GetBatch(ctx, "batch_1"); derrors.CodeOf(err) != derrors.CodeTimeout {
		t.Fatalf("err = %v, want the deadline during retries", err)
	}
	if !body.closed.Load() {
		t.Fatal("the response Do returned with the error was not closed")
	}
}

func TestNilClientIsNotReady(t *testing.T) {
	var c *Client
	if _, err := c.GetBatch(testCtx(t), "b"); derrors.CodeOf(err) != derrors.CodeNotReady {
		t.Fatalf("GetBatch: %v", err)
	}
	if _, err := c.SubmitBatch(testCtx(t), "m", "", nil); derrors.CodeOf(err) != derrors.CodeNotReady {
		t.Fatalf("SubmitBatch: %v", err)
	}
}
