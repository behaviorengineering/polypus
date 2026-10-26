package config

import "testing"

func TestLoadRouterAnthropicExtension(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "secret-key-at-least-twenty-ch")

	dir := t.TempDir()
	path := dir + "/config.yaml"
	content := `
chat_backend:
  enabled: true
  default: claude_team
backends:
  claude_team:
    remote: true
    extension: anthropic
    auth:
      bearer_env: ANTHROPIC_API_KEY
    capabilities: [chat, vision]
  claude_nocache:
    remote: true
    extension: anthropic
    prompt_cache: false
    auth:
      bearer_env: ANTHROPIC_API_KEY
    capabilities: [chat]
`
	if err := writeTestFile(path, content); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	cfg, err := LoadRouterConfig(ServeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Backends["claude_team"]
	if !b.Remote || !b.IsAnthropicExtension() {
		t.Fatalf("backend: %+v", b)
	}
	if b.BaseURL != "" {
		t.Fatalf("anthropic base_url should stay empty for Bifrost default, got %q", b.BaseURL)
	}
	if !b.AutoPromptCache() {
		t.Fatal("prompt cache should default on for anthropic")
	}
	if cfg.Backends["claude_nocache"].AutoPromptCache() {
		t.Fatal("prompt_cache: false should turn automatic caching off")
	}
}

func TestAutoPromptCacheOnlyForAnthropic(t *testing.T) {
	on := true
	b := BackendDef{Extension: ExtensionGemini, PromptCache: &on}
	if b.AutoPromptCache() {
		t.Fatal("automatic caching applies to anthropic backends only")
	}
}
