//go:build integration

package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
)

const smokeBatchCustomID = "smoke-batch-1"

func startMockCloudflare() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(mockCloudflareHandler))
}

func mockCloudflareHandler(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	path := r.URL.Path
	switch {
	case strings.HasSuffix(path, "/ai/models/search"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"result":[],"result_info":{"page":1,"total_pages":1}}`))
		return
	case r.URL.Query().Get("queueRequest") == "true":
		handleBatchRun(w, r)
		return
	case strings.HasSuffix(path, "/ai/v1/chat/completions"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		return
	case strings.HasSuffix(path, "/ai/run") && !strings.Contains(path, "/ai/run/"):
		handleSystemOneRun(w, r)
		return
	case strings.Contains(path, "/ai/run/"):
		handleWorkersRun(w, r)
		return
	default:
		http.NotFound(w, r)
	}
}

func handleBatchRun(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	body := string(raw)
	w.Header().Set("Content-Type", "application/json")
	if strings.Contains(body, "request_id") {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result": map[string]interface{}{
				"responses": []map[string]interface{}{
					{
						"external_reference": smokeBatchCustomID,
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
			"request_id": "cf-smoke-1",
		},
	})
}

func handleSystemOneRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"result": map[string]interface{}{
			"model": "jev-test",
			"answers": map[string]interface{}{
				"is_urgent": map[string]interface{}{"type": "noul", "noul": 0.9},
			},
			"usage": map[string]int{"input_tokens": 1, "output_tokens": 1},
		},
	})
}

func handleWorkersRun(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.Contains(path, "nova-3") {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"result":{"text":"here is what the file shows for this episode"}}`))
		return
	}
	if strings.Contains(path, "aura-2-en") {
		w.Header().Set("Content-Type", "audio/mpeg")
		audio := make([]byte, 128)
		for i := range audio {
			audio[i] = 'a'
		}
		_, _ = w.Write(audio)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"success":true,"result":{"response":"ok"}}`))
}
