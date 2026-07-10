package router

import "testing"

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

func TestOpenAIBaseURL(t *testing.T) {
	if got := OpenAIBaseURL("http://127.0.0.1:1322/"); got != "http://127.0.0.1:1322" {
		t.Fatalf("got %q", got)
	}
}
