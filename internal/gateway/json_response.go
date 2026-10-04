package gateway

import (
	"encoding/json"
	"net/http"
)

func writeOpenAIJSON(w http.ResponseWriter, status int, body openaiErrorBody) {
	raw, err := json.Marshal(body)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}
