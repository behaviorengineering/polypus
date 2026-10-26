package cloudflare

import (
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestClientsReuseSameInstance(t *testing.T) {
	cs := &Clients{}
	t.Setenv("CF_CACHE_KEY", "secret-a")
	b := config.BackendDef{
		ID:        "cf_local",
		Remote:    true,
		Extension: config.ExtensionCloudflare,
		BaseURL:   "https://api.cloudflare.com/client/v4/accounts/test-acc/ai/v1",
		Auth:      config.BackendAuth{BearerEnv: "CF_CACHE_KEY"},
	}
	a, err := cs.Get(b)
	if err != nil {
		t.Fatal(err)
	}
	again, err := cs.Get(b)
	if err != nil {
		t.Fatal(err)
	}
	if a != again {
		t.Fatal("expected same cached client")
	}
}

func TestClientsNewInstanceOnTokenChange(t *testing.T) {
	cs := &Clients{}
	t.Setenv("CF_CACHE_KEY", "secret-a")
	b := config.BackendDef{
		ID:        "cf_local",
		Remote:    true,
		Extension: config.ExtensionCloudflare,
		BaseURL:   "https://api.cloudflare.com/client/v4/accounts/test-acc/ai/v1",
		Auth:      config.BackendAuth{BearerEnv: "CF_CACHE_KEY"},
	}
	first, err := cs.Get(b)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CF_CACHE_KEY", "secret-b")
	second, err := cs.Get(b)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("expected new client after token change")
	}
}

func TestClientsRejectNonCloudflare(t *testing.T) {
	cs := &Clients{}
	_, err := cs.Get(config.BackendDef{
		ID:        "leaf",
		BaseURL:   "http://127.0.0.1:9",
		Extension: "",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
