package gateway

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var traceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// traceDumps is GET /debug/failures/<trace> when the trace also has stream-chunk
// dumps. A trace with only a failure dump gets that dump as it is.
type traceDumps struct {
	TraceID string `json:"trace_id"`
	// Failure is the request's failure dump, when it has one.
	Failure json.RawMessage `json:"failure,omitempty"`
	// StreamChunks are the chunks a stream dropped, in chunk order.
	StreamChunks []streamChunkDump `json:"stream_chunks"`
}

// streamChunkDump is one dropped-chunk dump file.
type streamChunkDump struct {
	File string `json:"file"`
	Dump any    `json:"dump"`
}

// serveFailureDump answers GET /debug/failures/<trace> (the route table admits
// nothing else) with every dump file of that trace.
func (h healthHandler) serveFailureDump(w http.ResponseWriter, r *http.Request) {
	traceID := strings.ToLower(strings.TrimPrefix(r.URL.Path, pathDebugFailures))
	if !traceIDPattern.MatchString(traceID) {
		http.Error(w, "invalid trace_id", http.StatusBadRequest)
		return
	}
	dumpDir := strings.TrimSpace(h.dumpDir)
	if dumpDir == "" {
		http.Error(w, "failure dumps disabled", http.StatusServiceUnavailable)
		return
	}
	failure, err := readTraceDump(dumpDir, traceID+".json")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		h.log.Warn("polypus: read failure dump", "trace_id", traceID, "dir", dumpDir, "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	chunks, err := readChunkDumps(dumpDir, traceID)
	if err != nil {
		h.log.Warn("polypus: read stream chunk dumps", "trace_id", traceID, "dir", dumpDir, "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if len(chunks) == 0 {
		if failure == nil {
			http.NotFound(w, r)
			return
		}
		h.writeDump(w, traceID, failure)
		return
	}
	body, err := json.Marshal(traceDumps{TraceID: traceID, Failure: validJSONOrNil(failure), StreamChunks: chunks})
	if err != nil {
		h.log.Warn("polypus: encode trace dumps", "trace_id", traceID, "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	h.writeDump(w, traceID, body)
}

func (h healthHandler) writeDump(w http.ResponseWriter, traceID string, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil { //nolint:gosec // G705: a JSON dump served as application/json with nosniff
		h.log.Debug("polypus: failure dump write failed", "trace_id", traceID, "err", err)
	}
}

// readTraceDump reads one dump file of dir. name comes from a trace id that matched
// traceIDPattern or from a listing of dir, so the path stays in dir.
func readTraceDump(dir, name string) ([]byte, error) {
	cleanDir := filepath.Clean(dir)
	path := filepath.Join(cleanDir, name)
	if filepath.Dir(path) != cleanDir {
		return nil, fs.ErrNotExist
	}
	return os.ReadFile(path) //nolint:gosec // G304: the path is checked to stay in the dump dir
}

// readChunkDumps returns the trace's stream-chunk dumps in chunk order.
func readChunkDumps(dir, traceID string) ([]streamChunkDump, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	prefix := traceID + chunkDumpInfix
	var out []streamChunkDump
	// ReadDir sorts by name, and the names pad the chunk index.
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, err := readTraceDump(dir, name)
		if err != nil {
			return nil, err
		}
		var dump any = string(raw)
		if json.Valid(raw) {
			dump = json.RawMessage(raw)
		}
		out = append(out, streamChunkDump{File: name, Dump: dump})
	}
	return out, nil
}

// validJSONOrNil keeps a failure dump that is valid JSON; a nil or corrupt one is
// left out of the combined body rather than breaking it.
func validJSONOrNil(raw []byte) json.RawMessage {
	if raw == nil || !json.Valid(raw) {
		return nil
	}
	return raw
}
