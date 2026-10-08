package gateway

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

func encodeJSON(w http.ResponseWriter, v any) {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("polypus: json response encode failed", "err", err)
	}
}
