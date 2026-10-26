package gateway

import (
	"encoding/json"
	"net/http"
)

func (s *shared) encodeJSON(w http.ResponseWriter, v any) {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.log.Warn("polypus: json response encode failed", "err", err)
	}
}
