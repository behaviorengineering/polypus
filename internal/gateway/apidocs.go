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
func serveEmbedded(contentType string, doc []byte) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		if _, err := w.Write(doc); err != nil {
			return // the client went away mid-body; nothing is left to tell it.
		}
	}
}

var (
	serveOpenAPISpec     = serveEmbedded("application/yaml", openAPISpecYAML)
	serveSystemOneSchema = serveEmbedded("application/json", systemOneSchemaJSON)
)
