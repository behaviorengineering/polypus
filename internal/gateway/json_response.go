package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
)

// keyModel is the OpenAI "model" key: a request and response field, an error
// param, and the object type of a model entry.
const keyModel = "model"

// objectList is the OpenAI "object" value of a list response.
const objectList = "list"

// jsonNull is the JSON null literal.
const jsonNull = "null"

// isJSONNull reports whether raw is absent or the JSON null.
func isJSONNull(raw []byte) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == jsonNull
}

// writeJSON is the gateway's one JSON response writer. It marshals before writing,
// so a value that cannot be encoded still gets a 500, and it logs a failed write.
func (s *shared) writeJSON(w http.ResponseWriter, status int, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		s.log.Warn("polypus: json response encode failed", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(append(raw, '\n')); err != nil {
		s.log.Warn("polypus: json response write failed", "err", err)
	}
}

// writeOpenAIError answers with an OpenAI error body.
func (s *shared) writeOpenAIError(w http.ResponseWriter, status int, e openaiError) {
	s.writeJSON(w, status, openaiErrorBody{Error: e})
}
