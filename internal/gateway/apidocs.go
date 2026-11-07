package gateway

import (
	_ "embed"
	"net/http"
)

//go:embed apidocs/openai.yaml
var openAPISpecYAML []byte

//go:embed apidocs/systemone.schema.json
var systemOneSchemaJSON []byte

// serveEmbedded answers GET and HEAD (the route table admits nothing else) with an
// embedded document.
func (rs responder) serveEmbedded(w http.ResponseWriter, r *http.Request, contentType string, doc []byte) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(doc); err != nil {
		// The client went away mid-body; nothing is left to tell it.
		rs.log.Debug("polypus: embedded document write failed", "path", r.URL.Path, "err", err)
	}
}

func (rs responder) serveOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	rs.serveEmbedded(w, r, "application/yaml", openAPISpecYAML)
}

func (rs responder) serveSystemOneSchema(w http.ResponseWriter, r *http.Request) {
	rs.serveEmbedded(w, r, "application/json", systemOneSchemaJSON)
}
