//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
)

func startMockSwitchyard() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(mockSwitchyardHandler))
}

func mockSwitchyardHandler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/health":
		w.WriteHeader(http.StatusOK)
	case "/v1/chat/completions":
		var req struct {
			Model string `json:"model"`
		}
		// Any router/<name> model is accepted, so env can override the default router.
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !strings.HasPrefix(req.Model, "router/") {
			http.Error(w, "unexpected model in body", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-model-router-selected-model", "cf_local/@cf/google/gemma-4-26b-a4b-it")
		_, _ = w.Write([]byte(`{"model":"cf_local/@cf/google/gemma-4-26b-a4b-it","choices":[{"message":{"content":"via-switchyard"}}]}`))
	default:
		http.NotFound(w, r)
	}
}
