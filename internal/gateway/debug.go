package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/behaviorengineering/polypus/internal/observability"
)

var traceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (h healthHandler) serveFailureDump(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	const prefix = "/debug/failures/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	traceID := strings.ToLower(strings.TrimPrefix(r.URL.Path, prefix))
	if !traceIDPattern.MatchString(traceID) {
		http.Error(w, "invalid trace_id", http.StatusBadRequest)
		return
	}
	dumpDir := observability.FailureDumpDir()
	if strings.TrimSpace(dumpDir) == "" {
		http.Error(w, "failure dumps disabled", http.StatusServiceUnavailable)
		return
	}
	path := filepath.Join(dumpDir, traceID+".json")
	cleanDir := filepath.Clean(dumpDir)
	cleanPath := filepath.Clean(path)
	if !strings.HasPrefix(cleanPath, cleanDir+string(os.PathSeparator)) && cleanPath != cleanDir {
		http.Error(w, "invalid trace_id", http.StatusBadRequest)
		return
	}
	body, err := os.ReadFile(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
