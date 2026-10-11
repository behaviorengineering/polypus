package gemini

import (
	"fmt"
	"sync"

	"github.com/behaviorengineering/polypus/internal/config"
)

var (
	clientMu    sync.Mutex
	clientCache = map[string]*Client{}
)

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
	key := clientCacheKey(b.ID, token, listURL)

	clientMu.Lock()
	defer clientMu.Unlock()
	if c, ok := clientCache[key]; ok {
		return c, nil
	}
	c, err := newClient(listURL, token)
	if err != nil {
		return nil, err
	}
	clientCache[key] = c
	return c, nil
}

// resetClientCacheForTest clears the process client cache (tests only).
func resetClientCacheForTest() {
	clientMu.Lock()
	defer clientMu.Unlock()
	clientCache = map[string]*Client{}
}
