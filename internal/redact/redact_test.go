package redact

import (
	"strings"
	"testing"
)

func TestURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		leakage string
	}{
		{name: "empty", raw: "", want: ""},
		{name: "clean", raw: "https://host.example/v1?alt=sse", want: "https://host.example/v1?alt=sse"},
		{name: "user and password", raw: "https://user:s3cret@host.example/v1", want: "https://REDACTED@host.example/v1", leakage: "s3cret"},
		{name: "token as user", raw: "https://s3cret@host.example/v1?alt=sse", want: "https://REDACTED@host.example/v1?alt=sse", leakage: "s3cret"},
		{name: "query key", raw: "https://host.example/v1?key=s3cret&alt=sse", want: "https://host.example/v1?alt=sse&key=REDACTED", leakage: "s3cret"},
		{name: "suffix token", raw: "https://host.example/v1?access_token=s3cret", want: "https://host.example/v1?access_token=REDACTED", leakage: "s3cret"},
		{name: "unparseable", raw: "http://[::1", want: "http://[::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := URL(tt.raw)
			if got != tt.want {
				t.Fatalf("URL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
			if tt.leakage != "" && strings.Contains(got, tt.leakage) {
				t.Fatalf("URL(%q) leaked %q: %s", tt.raw, tt.leakage, got)
			}
		})
	}
}

func TestQuery(t *testing.T) {
	if got := Query("key=s3cret&alt=sse"); got != "alt=sse&key=REDACTED" {
		t.Fatalf("Query = %q", got)
	}
	if got := Query("alt=sse"); got != "alt=sse" {
		t.Fatalf("clean query changed: %q", got)
	}
}

func TestText(t *testing.T) {
	got := Text(`Post "https://user:s3cret@host.example/v1": dial tcp: refused`)
	if strings.Contains(got, "s3cret") || !strings.Contains(got, "host.example/v1") {
		t.Fatalf("Text = %q", got)
	}
	if got := Text("no urls here"); got != "no urls here" {
		t.Fatalf("Text changed plain text: %q", got)
	}
}
