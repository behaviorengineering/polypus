package images

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeManifestOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/acme/widget/manifests/1.2.3" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	host := srv.URL[len("http://"):]
	ref := host + "/acme/widget:1.2.3"

	if err := ProbeManifest(testContext(t), ref, ""); err != nil {
		t.Fatal(err)
	}
}

func TestProbeManifest404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	host := srv.URL[len("http://"):]
	ref := host + "/acme/widget:9.9.9"

	err := ProbeManifest(testContext(t), ref, "")
	if !errors.Is(err, ErrManifestNotFound) {
		t.Fatalf("expected ErrManifestNotFound, got %v", err)
	}
}

func TestProbeManifest401WithoutToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	host := srv.URL[len("http://"):]
	ref := host + "/acme/widget:1.0.0"

	err := ProbeManifest(testContext(t), ref, "")
	if !errors.Is(err, ErrRegistryUnauthorized) {
		t.Fatalf("expected ErrRegistryUnauthorized, got %v", err)
	}
}

func TestProbeManifest503Retries(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	host := srv.URL[len("http://"):]
	ref := host + "/acme/widget:1.0.0"

	if err := ProbeManifest(testContext(t), ref, ""); err != nil {
		t.Fatal(err)
	}
	if hits < 2 {
		t.Fatalf("expected retry, hits=%d", hits)
	}
}
