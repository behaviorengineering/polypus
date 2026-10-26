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
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/batch"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
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
