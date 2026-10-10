package router

import (
	"os"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestRegistryFromPOLYPUS_CONFIG(t *testing.T) {
	path := strings.TrimSpace(os.Getenv("POLYPUS_CONFIG"))
	if path == "" {
		t.Skip("POLYPUS_CONFIG unset")
	}
	cfg, err := config.LoadRouterConfig(config.ServeOptions{})
	if err != nil {
		t.Fatalf("LoadRouterConfig: %v", err)
	}
	if _, err = NewRegistry(cfg); err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
}
