package gemini

import (
	"fmt"

	"github.com/behaviorengineering/polypus/internal/clients/catalog"
	"github.com/behaviorengineering/polypus/internal/config"
)

var clients catalog.Registry[Client]

func clientCacheKey(backendID, token, listURL string) string {
	return backendID + "\x00" + token + "\x00" + listURL
}

// GetClient returns a process-scoped Gemini catalog client for b.
func GetClient(b config.BackendDef) (*Client, error) {
	if !b.IsGeminiExtension() {
		return nil, fmt.Errorf("%w: %q", errNotGeminiExtension, b.ID)
	}
	token, err := b.Auth.ResolveBearerToken()
	if err != nil {
		return nil, err
	}
	listURL := openAIModelsListURL(b.BaseURL)
	return clients.Get(clientCacheKey(b.ID, token, listURL), func() (*Client, error) {
		return newClient(listURL, token)
	})
}

// resetClientCacheForTest clears the process client cache (tests only).
func resetClientCacheForTest() {
	clients.Reset()
}
