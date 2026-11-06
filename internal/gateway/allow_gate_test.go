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
	s := &shared{routerParts: routerParts{registry: reg}}
	if !s.ensureModelAllowed("cf", "@cf/a") {
		t.Fatal("an allowed model on a configured backend must pass")
	}
	if s.ensureModelAllowed("cf", "@cf/other") {
		t.Fatal("a model outside models.allow must be refused")
	}
	if s.ensureModelAllowed("ghost", "@cf/a") {
		t.Fatal("a backend missing from config must be refused, not waved through")
	}
	if s.ensureSystemOneAllowed("ghost", "ghost/typesafe/jev", "typesafe/jev") {
		t.Fatal("the SystemOne gate must refuse a backend missing from config")
	}
}

func TestEnsureModelAllowedWithoutRegistryRefuses(t *testing.T) {
	var nilShared *shared
	for name, s := range map[string]*shared{"nil shared": nilShared, "no registry": {}} {
		if s.ensureModelAllowed("cf", "@cf/a") {
			t.Fatalf("%s: the allow gate must fail closed", name)
		}
		if s.ensureSystemOneAllowed("cf", "cf/@cf/a", "@cf/a") {
			t.Fatalf("%s: the SystemOne gate must fail closed", name)
		}
	}
}
