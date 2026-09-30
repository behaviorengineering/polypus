//go:build integration

package integration

import (
	"io"
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
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"router/investigator"`) &&
			!strings.Contains(string(body), `"model": "router/investigator"`) {
			// Allow other router model names when env overrides the default.
			if !strings.Contains(string(body), `"model":"router/`) && !strings.Contains(string(body), `"model": "router/`) {
				http.Error(w, "unexpected model in body", http.StatusBadRequest)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"via-switchyard"}}]}`))
	default:
		http.NotFound(w, r)
	}
}
