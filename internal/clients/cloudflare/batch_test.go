package cloudflare

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/batch"
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
	c, err := newClient(apiBase, "token")
	if err != nil {
		t.Fatal(err)
	}
	lines := []batch.InputLine{{
		CustomID: "c1",
		Body:     []byte(`{"model":"@cf/meta/llama-3.3-70b-instruct-fp8-fast","messages":[{"role":"user","content":"hi"}]}`),
	}}
	id, err := c.SubmitBatch(context.Background(), "@cf/meta/llama-3.3-70b-instruct-fp8-fast", batch.EndpointChatCompletions, lines)
	if err != nil {
		t.Fatal(err)
	}
	if id != "cf-req-1" {
		t.Fatalf("request id: %s", id)
	}
	poll, err := c.PollBatch(context.Background(), "@cf/meta/llama-3.3-70b-instruct-fp8-fast", id)
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
