package gateway

import (
	"encoding/json"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// bodyModel reads the "model" field of a JSON request body. required makes an
// empty model an error.
func bodyModel(op string, body []byte, required bool) (string, error) {
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", derrors.Wrap(err, derrors.CodeInvalid, op, "invalid json")
	}
	model := strings.TrimSpace(payload.Model)
	if required && model == "" {
		return "", derrors.New(derrors.CodeInvalid, op, "model required")
	}
	return model, nil
}

// withBodyModel returns body with "model" set to model. Every other field passes
// through byte for byte.
func withBodyModel(op string, body []byte, model string) ([]byte, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, op, "invalid json")
	}
	raw, err := json.Marshal(model)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, op, "marshal model")
	}
	payload[keyModel] = raw
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, op, "marshal")
	}
	return out, nil
}
