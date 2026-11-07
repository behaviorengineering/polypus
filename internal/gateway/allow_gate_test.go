package gateway

import (
	"testing"

	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

func TestEnsureModelAllowedUnknownBackendRefuses(t *testing.T) {
	reg, err := router.NewRegistry(gatedAdminBackendConfig("http://example.invalid/v1"))
	if err != nil {
		t.Fatal(err)
	}
	if !ensureModelAllowed(reg, "cf", "@cf/a") {
		t.Fatal("an allowed model on a configured backend must pass")
	}
	if ensureModelAllowed(reg, "cf", "@cf/other") {
		t.Fatal("a model outside models.allow must be refused")
	}
	if ensureModelAllowed(reg, "ghost", "@cf/a") {
		t.Fatal("a backend missing from config must be refused, not waved through")
	}
	if ensureSystemOneAllowed(reg, "ghost", "ghost/typesafe/jev", "typesafe/jev") {
		t.Fatal("the SystemOne gate must refuse a backend missing from config")
	}
}
