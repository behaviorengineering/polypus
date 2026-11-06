package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// keyModel is the OpenAI "model" key: a request and response field, an error
// param, and the object type of a model entry.
const keyModel = "model"

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

// writeJSON is the gateway's one JSON response writer. It marshals before writing,
// so a value that cannot be encoded still gets a 500, and it logs a failed write.
func (s *shared) writeJSON(w http.ResponseWriter, status int, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		s.log.Warn("polypus: json response encode failed", "type", fmt.Sprintf("%T", v), "status", status, "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(append(raw, '\n')); err != nil {
		s.log.Warn("polypus: json response write failed", "type", fmt.Sprintf("%T", v), "status", status, "err", err)
	}
}

// writeOpenAIError answers with an OpenAI error body.
func (s *shared) writeOpenAIError(w http.ResponseWriter, status int, e openaiError) {
	s.writeJSON(w, status, openaiErrorBody{Error: e})
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
func (s *shared) writeMethodNotAllowed(w http.ResponseWriter, allow ...string) {
	w.Header().Set("Allow", strings.Join(allow, ", "))
	s.writeOpenAIError(w, http.StatusMethodNotAllowed, openaiError{
		Message: strings.ToLower(http.StatusText(http.StatusMethodNotAllowed)),
		Type:    errTypeInvalidRequest,
		Code:    codeMethodNotAllowed,
	})
}

// writeNotFound answers 404 with an OpenAI error body.
func (s *shared) writeNotFound(w http.ResponseWriter) {
	s.writeOpenAIError(w, http.StatusNotFound, openaiError{
		Message: strings.ToLower(http.StatusText(http.StatusNotFound)),
		Type:    errTypeNotFound,
		Code:    string(derrors.CodeNotFound),
	})
}
