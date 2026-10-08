package config

import (
	"testing"
)

func TestParseUIProxiesOK(t *testing.T) {
	proxies, err := ParseUIProxies([]uiProxyFile{
		{Path: "/phoenix", URL: "http://phoenix:6006"},
		{Path: "hyperdx", URL: "http://hyperdx:8080", Title: "HyperDX"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 2 {
		t.Fatalf("len %d", len(proxies))
	}
	// sorted longest prefix first (same length here, stable order)
	if proxies[0].Path != "/hyperdx" && proxies[0].Path != "/phoenix" {
		t.Fatalf("paths: %#v", proxies)
	}
}

func TestParseUIProxiesReservedPrefix(t *testing.T) {
	_, err := ParseUIProxies([]uiProxyFile{{Path: "/v1/foo", URL: "http://x:1"}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseUIProxiesDuplicatePath(t *testing.T) {
	_, err := ParseUIProxies([]uiProxyFile{
		{Path: "/phoenix", URL: "http://a:1"},
		{Path: "/phoenix", URL: "http://b:2"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
