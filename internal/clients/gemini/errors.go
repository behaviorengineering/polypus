package gemini

import "errors"

var (
	errAPIKeyRequired        = errors.New("gemini: api_key required")
	errNotGeminiExtension    = errors.New("gemini: backend is not a gemini extension")
	errListModelsURLRequired = errors.New("gemini: list models url required")
)
