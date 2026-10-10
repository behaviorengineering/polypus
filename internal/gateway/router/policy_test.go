package router

import (
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestValidateBackendURLLoopback(t *testing.T) {
	if err := validateBackendURL("http://127.0.0.1:1322"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateBackendURLRejectsOpenAI(t *testing.T) {
	if err := validateBackendURL("https://api.openai.com/v1"); err == nil {
		t.Fatal("expected cloud host rejection")
	}
}

func TestValidateBackendURLRejectsPrivateLAN(t *testing.T) {
	if err := validateBackendURL("http://192.168.1.50:8000"); err == nil {
		t.Fatal("expected private LAN host rejection")
	}
}

func TestValidateRemoteBackendAllowedWithoutCloudEnv(t *testing.T) {
	t.Setenv("CF_AI_API_KEY", "secret")
	b := config.BackendDef{
		Remote:  true,
		BaseURL: "https://api.cloudflare.com/client/v4/accounts/x/ai/v1",
		Auth:    config.BackendAuth{BearerEnv: "CF_AI_API_KEY"},
	}
	if err := ValidateBackend(b, config.DefaultRouterPolicy()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateLocalBackendAllowsLANWhenPolicyOff(t *testing.T) {
	b := config.BackendDef{
		BaseURL: "http://192.168.1.50:8000",
	}
	policy := config.RouterPolicy{RejectNonLoopbackBackends: false}
	if err := ValidateBackend(b, policy); err != nil {
		t.Fatal(err)
	}
}

func TestValidateGeminiBackendEmptyURL(t *testing.T) {
	b := config.BackendDef{
		Remote:    true,
		Extension: config.ExtensionGemini,
		Auth:      config.BackendAuth{BearerEnv: "GEMINI_API_KEY"},
	}
	if err := ValidateBackend(b, config.DefaultRouterPolicy()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateGeminiBackendGoogleHost(t *testing.T) {
	b := config.BackendDef{
		Remote:    true,
		Extension: config.ExtensionGemini,
		BaseURL:   "https://generativelanguage.googleapis.com/v1beta",
		Auth:      config.BackendAuth{BearerEnv: "GEMINI_API_KEY"},
	}
	if err := ValidateBackend(b, config.DefaultRouterPolicy()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRemoteRejectsGoogleHostWithoutGeminiExtension(t *testing.T) {
	b := config.BackendDef{
		Remote:  true,
		BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai",
		Auth:    config.BackendAuth{BearerEnv: "GEMINI_API_KEY"},
	}
	if err := ValidateBackend(b, config.DefaultRouterPolicy()); err == nil {
		t.Fatal("expected blocked google host for generic remote")
	}
}

func TestIsGeminiExtension(t *testing.T) {
	b := config.BackendDef{Extension: config.ExtensionGemini}
	if !b.IsGeminiExtension() {
		t.Fatal("expected gemini extension")
	}
}

func TestOpenAIBaseURL(t *testing.T) {
	if got := openAIBaseURL("http://127.0.0.1:1322/"); got != "http://127.0.0.1:1322" {
		t.Fatalf("got %q", got)
	}
	if got := openAIBaseURL("http://127.0.0.1:1234/v1"); got != "http://127.0.0.1:1234" {
		t.Fatalf("strip /v1: got %q", got)
	}
}
