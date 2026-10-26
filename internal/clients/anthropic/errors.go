package anthropic

import "errors"

var (
	errAPIKeyRequired        = errors.New("anthropic: api_key required")
	errNotAnthropicExtension = errors.New("anthropic: backend is not an anthropic extension")
	errListModelsURLRequired = errors.New("anthropic: list models url required")
)
