package cloudflare

import (
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestGetClientReusesSameInstance(t *testing.T) {
	t.Cleanup(resetClientCacheForTest)
	t.Setenv("CF_CACHE_KEY", "secret-a")
	b := config.BackendDef{
		ID:        "cf_local",
		Remote:    true,
		Extension: config.ExtensionCloudflare,
		BaseURL:   "https://api.cloudflare.com/client/v4/accounts/test-acc/ai/v1",
		Auth:      config.BackendAuth{BearerEnv: "CF_CACHE_KEY"},
	}
	a, err := GetClient(b)
	if err != nil {
		t.Fatal(err)
	}
	again, err := GetClient(b)
	if err != nil {
		t.Fatal(err)
	}
	if a != again {
		t.Fatal("expected same cached client")
	}
}

func TestGetClientDifferentTokenNewInstance(t *testing.T) {
	t.Cleanup(resetClientCacheForTest)
	t.Setenv("CF_CACHE_KEY", "secret-a")
	b := config.BackendDef{
		ID:        "cf_local",
		Remote:    true,
		Extension: config.ExtensionCloudflare,
		BaseURL:   "https://api.cloudflare.com/client/v4/accounts/test-acc/ai/v1",
		Auth:      config.BackendAuth{BearerEnv: "CF_CACHE_KEY"},
	}
	first, err := GetClient(b)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CF_CACHE_KEY", "secret-b")
	second, err := GetClient(b)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("expected new client after token change")
	}
}

func TestGetClientRejectsNonCloudflare(t *testing.T) {
	t.Cleanup(resetClientCacheForTest)
	_, err := GetClient(config.BackendDef{
		ID:        "leaf",
		BaseURL:   "http://127.0.0.1:9",
		Extension: "",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
