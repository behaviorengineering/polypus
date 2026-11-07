package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/behaviorengineering/polypus/internal/clients/catalog"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// keyModel is the OpenAI "model" key: a request and response field and an error
// param.
const keyModel = "model"

// objectModel is the OpenAI "object" value of a model entry, one name for every
// models surface.
const objectModel = catalog.ObjectModel

// OpenAI and provider chat body keys.
const (
	keyChatTemplateKwargs = "chat_template_kwargs"
	keyEnableThinking     = "enable_thinking"
	keyReasoningEffort    = "reasoning_effort"
	keyReasoning          = "reasoning"
	keyReasoningContent   = "reasoning_content"
	keyChoices            = "choices"
	keyFinishReason       = "finish_reason"
	keyExtraFields        = "extra_fields"
	keyEffort             = "effort"
	keyMaxTokens          = "max_tokens"
)

// objectList is the OpenAI "object" value of a list response.
const objectList = "list"

// jsonNull is the JSON null literal.
const jsonNull = "null"

// isJSONNull reports whether raw is absent or the JSON null.
func isJSONNull(raw []byte) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == jsonNull
}

// responder writes every response a route sends: JSON bodies, OpenAI errors and
// failure logs. Each handler has one; it is the only dependency they all share.
type responder struct {
	log *slog.Logger
}

// writeJSON is the gateway's one JSON response writer. It marshals before writing,
// so a value that cannot be encoded still gets a 500, and it logs a failed write.
func (rs responder) writeJSON(w http.ResponseWriter, status int, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		rs.log.Warn("polypus: json response encode failed", "type", fmt.Sprintf("%T", v), "status", status, "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(append(raw, '\n')); err != nil {
		rs.log.Warn("polypus: json response write failed", "type", fmt.Sprintf("%T", v), "status", status, "err", err)
	}
}

// writeOpenAIError answers with an OpenAI error body.
func (rs responder) writeOpenAIError(w http.ResponseWriter, status int, e openaiError) {
	rs.writeJSON(w, status, openaiErrorBody{Error: e})
}

// Wire-only OpenAI error codes with no derrors counterpart.
const (
	codeMethodNotAllowed    = "method_not_allowed"
	codeModelNotAllowed     = "model_not_allowed"
	codeModelNotFound       = "model_not_found"
	codeModelNotInInventory = "model_not_in_inventory"
	codeAPINotConfigured    = "api_not_configured"
)

// allowRead is the Allow list of a read-only route.
var allowRead = []string{http.MethodGet, http.MethodHead}

// writeMethodNotAllowed answers 405 with the Allow header RFC 9110 requires.
func (rs responder) writeMethodNotAllowed(w http.ResponseWriter, allow ...string) {
	w.Header().Set("Allow", strings.Join(allow, ", "))
	rs.writeOpenAIError(w, http.StatusMethodNotAllowed, openaiError{
		Message: strings.ToLower(http.StatusText(http.StatusMethodNotAllowed)),
		Type:    errTypeInvalidRequest,
		Code:    codeMethodNotAllowed,
	})
}

// writeNotFound answers 404 with an OpenAI error body.
func (rs responder) writeNotFound(w http.ResponseWriter) {
	rs.writeOpenAIError(w, http.StatusNotFound, openaiError{
		Message: strings.ToLower(http.StatusText(http.StatusNotFound)),
		Type:    errTypeNotFound,
		Code:    string(derrors.CodeNotFound),
	})
}
