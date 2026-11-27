package httpreq

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHostnameSanitization(t *testing.T) {
	t.Run("nil request", func(t *testing.T) {
		if got := Hostname(nil); got != "127.0.0.1" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("empty host", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http:///", nil)
		req.Host = ""
		req.URL.Host = ""
		if got := Hostname(req); got != "127.0.0.1" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("unsafe host", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "evil\nhost:1320"
		if got := Hostname(req); got != "127.0.0.1" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("strip port", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "gateway.example.com:1320"
		if got := Hostname(req); got != "gateway.example.com" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("ipv6 bracket fallback", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "[::1]:1320"
		if got := Hostname(req); got != "127.0.0.1" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestScheme(t *testing.T) {
	t.Run("forwarded proto first value", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Forwarded-Proto", "https, http")
		if got := Scheme(req); got != "https" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("default http", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if got := Scheme(req); got != "http" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("tls", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.TLS = &tls.ConnectionState{}
		if got := Scheme(req); got != "https" {
			t.Fatalf("got %q", got)
		}
	})
}
