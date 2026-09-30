//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
)

func startMockMLX() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(mockMLXHandler))
}

func mockMLXHandler(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && (r.URL.Path == "/" || r.URL.Path == ""):
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/audio/speech":
		w.Header().Set("Content-Type", "audio/mpeg")
		body := make([]byte, 128)
		for i := range body {
			body[i] = 'a'
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/audio/transcriptions":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "smoke transcript ok"})
	default:
		http.NotFound(w, r)
	}
}
