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

func TestOpenAIBaseURL(t *testing.T) {
	if got := openAIBaseURL("http://127.0.0.1:1322/"); got != "http://127.0.0.1:1322" {
		t.Fatalf("got %q", got)
	}
	if got := openAIBaseURL("http://127.0.0.1:1234/v1"); got != "http://127.0.0.1:1234" {
		t.Fatalf("strip /v1: got %q", got)
	}
}
