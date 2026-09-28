package cloudflare

import (
	"fmt"
	"sync"

	"github.com/behaviorengineering/polypus/internal/config"
)

var (
	clientMu    sync.Mutex
	clientCache = map[string]*Client{}
)

func clientCacheKey(backendID, token, apiBase string) string {
	return backendID + "\x00" + token + "\x00" + apiBase
}

// GetClient returns a process-scoped Cloudflare extension client for b.
// Clients are keyed by backend id, resolved bearer token, and API base URL so
// env token rotation after restart (or different backends) gets a fresh client.
func GetClient(b config.BackendDef) (*Client, error) {
	if !b.IsCloudflareExtension() {
		return nil, fmt.Errorf("%w: %q", errNotCloudflareExtension, b.ID)
	}
	token, err := b.Auth.ResolveBearerToken()
	if err != nil {
		return nil, err
	}
	apiBase, err := AIBaseURL(b.BaseURL)
	if err != nil {
		return nil, err
	}
	key := clientCacheKey(b.ID, token, apiBase)

	clientMu.Lock()
	defer clientMu.Unlock()
	if c, ok := clientCache[key]; ok {
		return c, nil
	}
	c, err := newClient(apiBase, token)
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
