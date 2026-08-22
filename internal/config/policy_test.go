package config

import (
	"testing"
)

func TestDefaultRouterPolicy(t *testing.T) {
	p := DefaultRouterPolicy()
	if !p.RejectNonLoopbackBackends || !p.RequireCloudOptIn {
		t.Fatalf("defaults: %+v", p)
	}
}

func TestRouterPolicyMergePartial(t *testing.T) {
	falseVal := false
	p := (routerPolicyFile{RejectNonLoopbackBackends: &falseVal}).merge()
	if p.RejectNonLoopbackBackends {
		t.Fatal("expected reject false")
	}
	if !p.RequireCloudOptIn {
		t.Fatal("expected cloud opt-in default true")
	}
}

func TestLoadRouterConfigPolicyBlock(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.yaml"
	content := `
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
policy:
  reject_non_loopback_backends: false
  require_cloud_opt_in: true
backends:
  mlx_local:
    base_url: http://192.168.1.50:8000
    capabilities: [tts, stt, voices]
`
	if err := writeTestFile(path, content); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	t.Setenv("INFERENCE_CLOUD_CASE", "0")

	cfg, err := LoadRouterConfig(ServeOptions{BackendURL: "http://127.0.0.1:1322"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Policy.RejectNonLoopbackBackends {
		t.Fatal("expected policy reject false")
	}
	if !cfg.Policy.RequireCloudOptIn {
		t.Fatal("expected require cloud opt-in true")
	}
}

func TestLoadRouterConfigPolicyNoCloudOptIn(t *testing.T) {
	t.Setenv("INFERENCE_CLOUD_CASE", "0")
	t.Setenv("CF_AI_API_KEY", "secret")
	t.Setenv("CF_ACCOUNT_ID", "acct")

	dir := t.TempDir()
	path := dir + "/config.yaml"
	content := `
default_chat_backend: cf_local
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
policy:
  require_cloud_opt_in: false
backends:
  cf_local:
    remote: true
    extension: cloudflare
    base_url: https://api.cloudflare.com/client/v4/accounts/${CF_ACCOUNT_ID}/ai/v1
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat]
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
`
	if err := writeTestFile(path, content); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	cfg, err := LoadRouterConfig(ServeOptions{BackendURL: "http://127.0.0.1:1322"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Backends["cf_local"]; !ok {
		t.Fatal("cf_local should remain when require_cloud_opt_in is false")
	}
}
