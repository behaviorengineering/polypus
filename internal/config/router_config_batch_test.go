package config

import "testing"

func TestFillDefaultBatchBackendOmittedEnablesCloudflare(t *testing.T) {
	t.Parallel()
	cfg := RouterConfig{
		Chat: CapabilityBackend{Enabled: true, Default: "cf_local"},
		Backends: map[string]BackendDef{
			"cf_local": {
				ID:           "cf_local",
				Extension:    ExtensionCloudflare,
				BaseURL:      "https://example.test/v1",
				Capabilities: []Capability{CapChat},
			},
		},
	}
	if err := normalizeRouterConfig(&cfg); err != nil {
		t.Fatalf("normalizeRouterConfig: %v", err)
	}
	if !cfg.Batch.Enabled || cfg.Batch.Default != "cf_local" {
		t.Fatalf("batch default = %+v, want enabled cf_local", cfg.Batch)
	}
	if !cfg.Backends["cf_local"].HasCapability(CapBatch) {
		t.Fatal("cf_local missing batch capability")
	}
}

func TestFillDefaultBatchBackendExplicitOff(t *testing.T) {
	t.Parallel()
	cfg := RouterConfig{
		batchSpecified: true,
		Batch:          CapabilityBackend{Enabled: false},
		Chat:           CapabilityBackend{Enabled: true, Default: "cf_local"},
		Backends: map[string]BackendDef{
			"cf_local": {
				ID:           "cf_local",
				Extension:    ExtensionCloudflare,
				BaseURL:      "https://example.test/v1",
				Capabilities: []Capability{CapChat},
			},
		},
	}
	if err := normalizeRouterConfig(&cfg); err != nil {
		t.Fatalf("normalizeRouterConfig: %v", err)
	}
	if cfg.Batch.Enabled {
		t.Fatal("batch stayed enabled despite enabled: false")
	}
	if cfg.Backends["cf_local"].HasCapability(CapBatch) {
		t.Fatal("explicit off should not add batch capability")
	}
}

func TestFillDefaultBatchBackendNoCloudflareStaysOff(t *testing.T) {
	t.Parallel()
	cfg := RouterConfig{
		TTS: CapabilityBackend{Enabled: true, Default: "mlx_local"},
		Backends: map[string]BackendDef{
			"mlx_local": {
				ID:           "mlx_local",
				BaseURL:      "http://127.0.0.1:1322",
				Capabilities: []Capability{CapTTS, CapSTT, CapVoices},
			},
		},
	}
	if err := normalizeRouterConfig(&cfg); err != nil {
		t.Fatalf("normalizeRouterConfig: %v", err)
	}
	if cfg.Batch.Enabled {
		t.Fatal("batch enabled without a Cloudflare backend")
	}
}

func TestFillDefaultBatchBackendExplicitOnAddsCapability(t *testing.T) {
	t.Parallel()
	cfg := RouterConfig{
		batchSpecified: true,
		Chat:           CapabilityBackend{Enabled: true, Default: "cf_local"},
		Batch:          CapabilityBackend{Enabled: true, Default: "cf_local"},
		Backends: map[string]BackendDef{
			"cf_local": {
				ID:           "cf_local",
				Extension:    ExtensionCloudflare,
				BaseURL:      "https://example.test/v1",
				Capabilities: []Capability{CapChat},
			},
		},
	}
	if err := normalizeRouterConfig(&cfg); err != nil {
		t.Fatalf("normalizeRouterConfig: %v", err)
	}
	if !cfg.Backends["cf_local"].HasCapability(CapBatch) {
		t.Fatal("enabled batch_backend should add batch capability on Cloudflare")
	}
}
