package smoke_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/smoke"
)

func TestSmokeAllChannels(t *testing.T) {
	t.Setenv("CF_AI_API_KEY", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/v1/chat/completions":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]string{"content": "ok"}}},
			})
		case "/v1/audio/speech":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write(bytesRepeat(128, 'a'))
		case "/v1/audio/transcriptions":
			_ = json.NewEncoder(w).Encode(map[string]string{"text": "here is what the file shows"})
		case "/v1/systemone":
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

func TestSystemOneDialsWithoutShellCFKey(t *testing.T) {
	t.Setenv("CF_AI_API_KEY", "")
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" {
			http.NotFound(w, r)
			return
		}
		hits++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "jev-test",
			"answers": map[string]any{"is_urgent": map[string]any{"type": "noul", "noul": 0.9}},
		})
	}))
	t.Cleanup(srv.Close)

	results, err := smoke.Run(context.Background(), smoke.Options{
		BaseURL:  srv.URL,
		Channels: []string{smoke.ChannelSystemOne},
	})
	if err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("gateway hits=%d results=%+v", hits, results)
	}
	if len(results) != 1 || results[0].Status != "pass" {
		t.Fatalf("%+v", results)
	}
}

func TestSystemOneSkipInsufficientBalanceLocal(t *testing.T) {
	t.Setenv("CF_AI_API_KEY", "")
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
	t.Setenv("CF_AI_API_KEY", "")
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

func TestTTSWritesAudioOutPath(t *testing.T) {
	dir := t.TempDir()
	out := dir + "/out.mp3"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(bytesRepeat(128, 'a'))
	}))
	t.Cleanup(srv.Close)

	_, err := smoke.Run(context.Background(), smoke.Options{
		BaseURL:      srv.URL,
		Channels:     []string{smoke.ChannelTTS},
		AudioOutPath: out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("audio out: %v", err)
	}
}

func TestBatchLifecycleSmoke(t *testing.T) {
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/health":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/files":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "file-smoke-1", "object": "file", "purpose": "batch",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/batches":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "batch-smoke-1", "object": "batch", "status": "in_progress",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/batches/batch-smoke-1":
			polls++
			if polls < 2 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id": "batch-smoke-1", "status": "in_progress",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "batch-smoke-1", "status": "completed", "output_file_id": "file-out-1",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/files/file-out-1/content":
			_, _ = w.Write([]byte(`{"id":"resp","custom_id":"smoke-batch-1","response":{"status_code":200,"body":{}}}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	results, err := smoke.Run(ctx, smoke.Options{
		BaseURL:  srv.URL,
		Channels: []string{smoke.ChannelBatch},
	})
	if err != nil {
		t.Fatalf("run: %v results=%v", err, results)
	}
	if polls < 2 {
		t.Fatalf("expected poll retries, polls=%d", polls)
	}
	for _, r := range results {
		if r.Status == "fail" {
			t.Fatalf("failed row: %+v", r)
		}
	}
}

func TestBatchRequiresDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	_, err := smoke.Run(context.Background(), smoke.Options{
		BaseURL:  srv.URL,
		Channels: []string{smoke.ChannelBatch},
	})
	if err == nil {
		t.Fatal("expected fail without deadline")
	}
}

func bytesRepeat(n int, b byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}
