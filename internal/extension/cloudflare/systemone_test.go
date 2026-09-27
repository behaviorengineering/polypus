package cloudflare

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSystemOneUnwrapsEnvelope(t *testing.T) {
	t.Parallel()
	const wantAnswers = `{"is_urgent":{"type":"noul","noul":0.95}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/run") || strings.Contains(r.URL.Path, "/run/") {
			t.Errorf("path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("auth %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		var m map[string]json.RawMessage
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatal(err)
		}
		if string(m["model"]) != `"typesafe/jev"` {
			t.Fatalf("model %s", m["model"])
		}
		var input map[string]json.RawMessage
		if err := json.Unmarshal(m["input"], &input); err != nil {
			t.Fatal(err)
		}
		if _, ok := input["model"]; ok {
			t.Fatal("model should not be nested under input")
		}
		if _, ok := input["state"]; !ok {
			t.Fatal("state missing")
		}
		if _, ok := input["questions"]; !ok {
			t.Fatal("questions missing")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"errors":  []any{},
			"result": map[string]any{
				"model":   "jev-1.13.0",
				"answers": json.RawMessage(wantAnswers),
				"usage":   map[string]int{"input_tokens": 10, "output_tokens": 5},
			},
		})
	}))
	t.Cleanup(srv.Close)

	c := &Client{
		apiBase:      srv.URL + "/client/v4/accounts/acct/ai",
		apiKey:       "test-key",
		speechClient: srv.Client(),
	}
	reqBody := []byte(`{
		"model": "cf_local/typesafe/jev",
		"state": "Help! payouts failing.",
		"questions": {
			"is_urgent": {
				"type": "noul",
				"instructions": "Does this convey urgency?"
			}
		}
	}`)
	out, err := c.SystemOne(context.Background(), "cf_local/typesafe/jev", reqBody)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Model   string          `json:"model"`
		Answers json.RawMessage `json:"answers"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "jev-1.13.0" {
		t.Fatalf("model %q", got.Model)
	}
	if string(got.Answers) != wantAnswers {
		t.Fatalf("answers %s", got.Answers)
	}
}

func TestSystemOneBareResponse(t *testing.T) {
	t.Parallel()
	bare := `{"model":"jev-1.13.0","answers":{"x":{"type":"noul","noul":0.5}},"usage":{"input_tokens":1,"output_tokens":1}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bare))
	}))
	t.Cleanup(srv.Close)

	c := &Client{
		apiBase:      srv.URL + "/client/v4/accounts/acct/ai",
		apiKey:       "k",
		speechClient: srv.Client(),
	}
	out, err := c.SystemOne(context.Background(), "typesafe/jev", []byte(`{"state":"s","questions":{"x":{"type":"noul","instructions":"?"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != bare {
		t.Fatalf("got %s", out)
	}
}

func TestSystemOneUnwrapsUnifiedGateway(t *testing.T) {
	t.Parallel()
	const wantAnswers = `{"is_urgent":{"noul":0.95,"type":"noul"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"gatewayMetadata": map[string]string{"keySource": "Unified"},
			"model":           "typesafe/jev",
			"result": map[string]any{
				"answers": json.RawMessage(wantAnswers),
				"model":   "jev-1.13.0",
				"usage":   map[string]int{"input_tokens": 307, "output_tokens": 23},
			},
			"state": "Completed",
		})
	}))
	t.Cleanup(srv.Close)

	c := &Client{
		apiBase:      srv.URL + "/client/v4/accounts/acct/ai",
		apiKey:       "k",
		speechClient: srv.Client(),
	}
	out, err := c.SystemOne(context.Background(), "typesafe/jev", []byte(`{"state":"s","questions":{"is_urgent":{"type":"noul","instructions":"?"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Model   string          `json:"model"`
		Answers json.RawMessage `json:"answers"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "jev-1.13.0" {
		t.Fatalf("model %q", got.Model)
	}
	if string(got.Answers) != wantAnswers {
		t.Fatalf("answers %s", got.Answers)
	}
}

func TestSystemOneUnwrapsClassicThenUnified(t *testing.T) {
	t.Parallel()
	const wantAnswers = `{"is_urgent":{"type":"noul","noul":0.95}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Live Cloudflare shape: classic success envelope around Unified gateway.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success":  true,
			"errors":   []any{},
			"messages": []any{},
			"result": map[string]any{
				"state": "Completed",
				"gatewayMetadata": map[string]string{
					"keySource": "Unified",
				},
				"result": map[string]any{
					"model":   "jev-1.13.0",
					"answers": json.RawMessage(wantAnswers),
					"usage":   map[string]int{"input_tokens": 307, "output_tokens": 23},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	c := &Client{
		apiBase:      srv.URL + "/client/v4/accounts/acct/ai",
		apiKey:       "k",
		speechClient: srv.Client(),
	}
	out, err := c.SystemOne(context.Background(), "typesafe/jev", []byte(`{"state":"s","questions":{"is_urgent":{"type":"noul","instructions":"?"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Answers map[string]json.RawMessage `json:"answers"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Answers["is_urgent"]; !ok {
		t.Fatalf("answers %s", out)
	}
}

func TestSystemOneSuccessFalse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"errors":  []map[string]string{{"message": "quota exceeded"}},
			"result":  nil,
		})
	}))
	t.Cleanup(srv.Close)

	c := &Client{
		apiBase:      srv.URL + "/client/v4/accounts/acct/ai",
		apiKey:       "k",
		speechClient: srv.Client(),
	}
	_, err := c.SystemOne(context.Background(), "typesafe/jev", []byte(`{"state":"s","questions":{"x":{"type":"noul","instructions":"?"}}}`))
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("err=%v", err)
	}
}

func TestSystemOneHTTPErrorUsesCFMessage(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"errors":  []map[string]any{{"message": "Insufficient balance; add money to your gateway or use BYOK", "code": 2021}},
			"result":  map[string]any{},
		})
	}))
	t.Cleanup(srv.Close)

	c := &Client{
		apiBase:      srv.URL + "/client/v4/accounts/acct/ai",
		apiKey:       "k",
		speechClient: srv.Client(),
	}
	_, err := c.SystemOne(context.Background(), "typesafe/jev", []byte(`{"state":"s","questions":{"x":{"type":"noul","instructions":"?"}}}`))
	if err == nil || !strings.Contains(err.Error(), "Insufficient balance") {
		t.Fatalf("err=%v", err)
	}
}

func TestStripModelField(t *testing.T) {
	t.Parallel()
	out, err := stripModelField([]byte(`{"model":"typesafe/jev","state":"hi","questions":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["model"]; ok {
		t.Fatal("model still present")
	}
	if _, ok := m["state"]; !ok {
		t.Fatal("state missing")
	}
}
