package cloudflare

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/batch"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

func TestSubmitAndPollBatch(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.Contains(r.URL.RawQuery, "queueRequest=true") {
			t.Fatalf("missing queueRequest: %s", r.URL.String())
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "request_id") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": map[string]interface{}{
					"responses": []map[string]interface{}{
						{
							"external_reference": "c1",
							"success":            true,
							"result":             map[string]string{"response": "ok"},
						},
					},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result": map[string]interface{}{
				"status":     "queued",
				"request_id": "cf-req-1",
			},
		})
	}))
	defer srv.Close()

	apiBase := srv.URL + "/accounts/acct/ai"
	c, err := newClient(apiBase, "token", time.Now, SpeechDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	lines := []batch.InputLine{{
		CustomID: "c1",
		Body:     []byte(`{"model":"@cf/meta/llama-3.3-70b-instruct-fp8-fast","messages":[{"role":"user","content":"hi"}]}`),
	}}
	id, err := c.SubmitBatch(hopCtx(t), "@cf/meta/llama-3.3-70b-instruct-fp8-fast", batch.EndpointChatCompletions, lines)
	if err != nil {
		t.Fatal(err)
	}
	if id != "cf-req-1" {
		t.Fatalf("request id: %s", id)
	}
	poll, err := c.PollBatch(hopCtx(t), "@cf/meta/llama-3.3-70b-instruct-fp8-fast", id)
	if err != nil {
		t.Fatal(err)
	}
	if poll.State != BatchPollCompleted || len(poll.Responses) != 1 {
		t.Fatalf("poll: %+v", poll)
	}
	if calls < 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestParsePollResultMapsLineError(t *testing.T) {
	raw := []byte(`{"result":{"responses":[{"external_reference":"x","success":false,"error":{"message":"boom"}}]}}`)
	poll, err := unwrapThenParse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if poll.State != BatchPollCompleted || len(poll.Responses) != 1 {
		t.Fatalf("poll: %+v", poll)
	}
	if poll.Responses[0].ErrorMessage != "boom" {
		t.Fatalf("message: %q", poll.Responses[0].ErrorMessage)
	}
}

func TestParsePollResultRejectsUnknownStatus(t *testing.T) {
	raw := []byte(`{"result":{"status":"weird_state"}}`)
	_, err := unwrapThenParse(raw)
	if err == nil {
		t.Fatal("expected error for unknown status")
	}
}

func TestParsePollResultEmptyStatusIsAnError(t *testing.T) {
	t.Parallel()
	cases := [][]byte{
		[]byte(`{}`),
		[]byte(`null`),
		[]byte(`{"request_id":"cf-req-1"}`),
		[]byte(`{"success":true}`),
		[]byte(`{"result":{}}`),
		[]byte(`{"success":true,"result":{}}`),
	}
	for _, raw := range cases {
		_, err := unwrapThenParse(raw)
		if derrors.CodeOf(err) != derrors.CodeUnavailable {
			t.Fatalf("parsePollResult(%s): err %v, want an unavailable error, not queued", raw, err)
		}
	}
}

func TestParsePollResultQueued(t *testing.T) {
	t.Parallel()
	poll, err := unwrapThenParse([]byte(`{"success":true,"result":{"status":"queued","request_id":"cf-req-1"}}`))
	if err != nil || poll.State != BatchPollQueued {
		t.Fatalf("poll %+v err %v, want queued", poll, err)
	}
}

func TestPollBatchNamesTheRequestOnAnEmptyAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"result":{}}`))
	}))
	defer srv.Close()
	_, err := batchTestClient(t, srv).PollBatch(hopCtx(t), "@cf/meta/llama-3.3-70b-instruct-fp8-fast", "cf-req-9")
	if derrors.CodeOf(err) != derrors.CodeUnavailable || derrors.Field(err, "request_id") != "cf-req-9" {
		t.Fatalf("err %+v, want unavailable naming request cf-req-9", err)
	}
}

// unwrapThenParse reads a poll reply the way PollBatch does: envelope first.
func unwrapThenParse(raw []byte) (BatchPollResult, error) {
	inner, err := unwrapBatchEnvelope(raw)
	if err != nil {
		return BatchPollResult{}, err
	}
	return parsePollResult(inner)
}

func TestEmbeddingInputToText(t *testing.T) {
	for raw, want := range map[string]string{`"hello"`: "hello", `["one"]`: "one"} {
		got, err := embeddingInputToText(json.RawMessage(raw))
		if err != nil || got != want {
			t.Fatalf("%s: got %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{``, `""`, `["a","b"]`, `[1]`, `42`, `{"x":1}`, `[" "]`} {
		if _, err := embeddingInputToText(json.RawMessage(raw)); err == nil {
			t.Fatalf("%q: want an error", raw)
		}
	}
}

func TestPostBatchRunMapsUpstream4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"no such model"}]}`))
	}))
	defer srv.Close()
	c, err := newClient(srv.URL+"/accounts/acct/ai", "token", time.Now, SpeechDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.PollBatch(hopCtx(t), "@cf/meta/llama-3.3-70b-instruct-fp8-fast", "req-1")
	if got := derrors.CodeOf(err); got != derrors.CodeNotFound {
		t.Fatalf("code %q, want not_found (err %v)", got, err)
	}
	if !strings.Contains(err.Error(), "no such model") {
		t.Fatalf("want Cloudflare's message, got %v", err)
	}
}

// batchTestClient is a client for srv with policies of its own, so its breaker
// counts only this test's calls and its retries back off in milliseconds.
func batchTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := newClient(srv.URL+"/accounts/acct/ai", "token", time.Now, SpeechDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	c.backendID = "cf_batch"
	c.policies = fastPolicies()
	return c
}

func TestPollBatchRetriesTransient5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "cf-req-1") {
			t.Errorf("every attempt must carry the request id, got %s", body)
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"result":{"status":"running"}}`))
	}))
	defer srv.Close()
	poll, err := batchTestClient(t, srv).PollBatch(hopCtx(t), "@cf/meta/llama-3.3-70b-instruct-fp8-fast", "cf-req-1")
	if err != nil {
		t.Fatalf("a transient 503 on a poll must be retried: %v", err)
	}
	if poll.State != BatchPollRunning || calls.Load() != 2 {
		t.Fatalf("poll %+v after %d calls; want running after 2", poll, calls.Load())
	}
}

func TestSubmitBatchIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	lines := []batch.InputLine{{CustomID: "c1", Body: []byte(`{"messages":[{"role":"user","content":"hi"}]}`)}}
	_, err := batchTestClient(t, srv).SubmitBatch(hopCtx(t), "@cf/meta/llama-3.3-70b-instruct-fp8-fast", batch.EndpointChatCompletions, lines)
	if derrors.CodeOf(err) != derrors.CodeUnavailable {
		t.Fatalf("want the 503 as unavailable: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("a submit is paid and goes once; got %d calls", calls.Load())
	}
}

// fastPolicies keeps production retry behaviour but backs off for a millisecond,
// so retry tests do not wait in real time.
func fastPolicies() *outbound.Policies {
	return outbound.NewPoliciesWith(outbound.Backoff{Min: time.Millisecond, Max: time.Millisecond})
}
