package router

import (
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestValidateBackendURLLoopback(t *testing.T) {
	if err := ValidateBackendURL("http://127.0.0.1:1322"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateBackendURLRejectsOpenAI(t *testing.T) {
	if err := ValidateBackendURL("https://api.openai.com/v1"); err == nil {
		t.Fatal("expected cloud host rejection")
	}
}

func TestValidateBackendURLRejectsPrivateLAN(t *testing.T) {
	if err := ValidateBackendURL("http://192.168.1.50:8000"); err == nil {
		t.Fatal("expected private LAN host rejection")
	}
}

func TestValidateRemoteBackendRequiresOptIn(t *testing.T) {
	t.Setenv("INFERENCE_CLOUD_CASE", "0")
	b := config.BackendDef{
		Remote:  true,
		BaseURL: "https://api.cloudflare.com/client/v4/accounts/x/ai/v1",
	}
	if err := ValidateBackend(b, config.DefaultRouterPolicy()); err == nil {
		t.Fatal("expected opt-in required")
	}
}

func TestValidateRemoteBackendAllowedWithOptIn(t *testing.T) {
	t.Setenv("INFERENCE_CLOUD_CASE", "1")
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
	policy := config.RouterPolicy{RejectNonLoopbackBackends: false, RequireCloudOptIn: true}
	if err := ValidateBackend(b, policy); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRemoteBackendWithoutCloudOptInPolicy(t *testing.T) {
	t.Setenv("INFERENCE_CLOUD_CASE", "0")
	b := config.BackendDef{
		Remote:  true,
		BaseURL: "https://api.cloudflare.com/client/v4/accounts/x/ai/v1",
		Auth:    config.BackendAuth{BearerEnv: "CF_AI_API_KEY"},
	}
	t.Setenv("CF_AI_API_KEY", "secret")
	policy := config.RouterPolicy{RejectNonLoopbackBackends: true, RequireCloudOptIn: false}
	if err := ValidateBackend(b, policy); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIBaseURL(t *testing.T) {
	if got := OpenAIBaseURL("http://127.0.0.1:1322/"); got != "http://127.0.0.1:1322" {
		t.Fatalf("got %q", got)
	}
}
