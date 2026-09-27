package smoke_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/smoke"
)

func TestSmokeAllChannels(t *testing.T) {
	t.Setenv("CF_AI_API_KEY", "test-key")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/health":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.URL.Path == "/v1/chat/completions":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]string{"content": "ok"}}},
			})
		case r.URL.Path == "/v1/audio/speech":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write(bytesRepeat(128, 'a'))
		case r.URL.Path == "/v1/audio/transcriptions":
			_ = json.NewEncoder(w).Encode(map[string]string{"text": "here is what the file shows"})
		case r.URL.Path == "/v1/systemone":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "is_urgent") {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model":   "jev-test",
				"answers": map[string]any{"is_urgent": map[string]any{"type": "noul", "noul": 0.9}},
				"usage":   map[string]int{"input_tokens": 1, "output_tokens": 1},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	results, err := smoke.Run(context.Background(), smoke.Options{
		BaseURL:   srv.URL,
		RequireCF: true,
	})
	if err != nil {
		t.Fatalf("run: %v results=%v", err, results)
	}
	if len(results) < 4 {
		t.Fatalf("expected results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status == "fail" {
			t.Fatalf("failed row: %+v", r)
		}
	}
}

func TestSystemOneSkipWithoutCF(t *testing.T) {
	t.Setenv("CF_AI_API_KEY", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "should not dial", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	results, err := smoke.Run(context.Background(), smoke.Options{
		BaseURL:  srv.URL,
		Channels: []string{smoke.ChannelSystemOne},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != "skip" {
		t.Fatalf("%+v", results)
	}
}

func TestSystemOneSkipInsufficientBalanceLocal(t *testing.T) {
	t.Setenv("CF_AI_API_KEY", "test-key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`cloudflare.SystemOne: Insufficient balance; add money to your gateway or use BYOK`))
	}))
	t.Cleanup(srv.Close)

	results, err := smoke.Run(context.Background(), smoke.Options{
		BaseURL:   srv.URL,
		Channels:  []string{smoke.ChannelSystemOne},
		RequireCF: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != "skip" {
		t.Fatalf("%+v", results)
	}
	if !strings.Contains(results[0].Detail, "Insufficient balance") {
		t.Fatalf("detail %q", results[0].Detail)
	}
}

func TestSystemOneFailInsufficientBalanceWhenRequireCF(t *testing.T) {
	t.Setenv("CF_AI_API_KEY", "test-key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`cloudflare.SystemOne: Insufficient balance; add money to your gateway or use BYOK`))
	}))
	t.Cleanup(srv.Close)

	_, err := smoke.Run(context.Background(), smoke.Options{
		BaseURL:   srv.URL,
		Channels:  []string{smoke.ChannelSystemOne},
		RequireCF: true,
	})
	if err == nil {
		t.Fatal("expected fail when RequireCF")
	}
}

func bytesRepeat(n int, b byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}
