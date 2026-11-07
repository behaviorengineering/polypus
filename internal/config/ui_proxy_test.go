package config

import (
	"testing"
	"unicode/utf8"
)

func TestParseUIProxiesOK(t *testing.T) {
	proxies, err := parseUIProxies([]uiProxyFile{
		{Path: "/phoenix", URL: "http://phoenix:6006"},
		{Path: "hyperdx", URL: "http://hyperdx:8080", Title: "HyperDX"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 2 {
		t.Fatalf("len %d", len(proxies))
	}
	// sorted longest prefix first (same length here, stable order).
	if proxies[0].Path != "/hyperdx" && proxies[0].Path != "/phoenix" {
		t.Fatalf("paths: %#v", proxies)
	}
}

func TestParseUIProxiesReservedPrefix(t *testing.T) {
	_, err := parseUIProxies([]uiProxyFile{{Path: "/v1/foo", URL: "http://x:1"}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseUIProxiesStripPrefixDefault(t *testing.T) {
	proxies, err := parseUIProxies([]uiProxyFile{
		{Path: "/phoenix", URL: "http://phoenix:6006"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !proxies[0].StripPrefix {
		t.Fatal("expected strip_prefix true by default")
	}
}

func TestParseUIProxiesStripPrefixFalse(t *testing.T) {
	falseVal := false
	proxies, err := parseUIProxies([]uiProxyFile{
		{Path: "/hyperdx", URL: "http://hyperdx:8080", StripPrefix: &falseVal},
	})
	if err != nil {
		t.Fatal(err)
	}
	if proxies[0].StripPrefix {
		t.Fatal("expected strip_prefix false")
	}
}

func TestParseUIProxiesDuplicatePath(t *testing.T) {
	_, err := parseUIProxies([]uiProxyFile{
		{Path: "/phoenix", URL: "http://a:1"},
		{Path: "/phoenix", URL: "http://b:2"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDefaultUIProxyTitleUpperCasesTheFirstRune(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]string{
		"/phoenix": "Phoenix",
		"/éval":    "Éval",
		"/":        "UI",
	} {
		got := defaultUIProxyTitle(path)
		if got != want || !utf8.ValidString(got) {
			t.Errorf("defaultUIProxyTitle(%q) = %q, want %q", path, got, want)
		}
	}
}
