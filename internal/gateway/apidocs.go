package gateway

import (
	_ "embed"
	"net/http"
)

//go:embed apidocs/openai.yaml
var openAPISpecYAML []byte

//go:embed apidocs/systemone.schema.json
var systemOneSchemaJSON []byte

// serveEmbedded answers GET and HEAD with an embedded document.
func (s *shared) serveEmbedded(w http.ResponseWriter, r *http.Request, contentType string, doc []byte) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.writeMethodNotAllowed(w, allowRead...)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(doc); err != nil {
		// The client went away mid-body; nothing is left to tell it.
		s.log.Debug("polypus: embedded document write failed", "path", r.URL.Path, "err", err)
	}
}

func (s *shared) serveOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	s.serveEmbedded(w, r, "application/yaml", openAPISpecYAML)
}

func (s *shared) serveSystemOneSchema(w http.ResponseWriter, r *http.Request) {
	s.serveEmbedded(w, r, "application/json", systemOneSchemaJSON)
}
