package images

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistryBearerTokenUsesBasicAuth(t *testing.T) {
	const wantUser = "token"
	const wantPass = "ghp_test_pat"
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		if !strings.HasPrefix(gotAuth, "Basic ") {
			http.Error(w, "expected basic", http.StatusUnauthorized)
			return
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(gotAuth, "Basic "))
		if err != nil {
			http.Error(w, "bad basic", http.StatusUnauthorized)
			return
		}
		parts := strings.SplitN(string(raw), ":", 2)
		if len(parts) != 2 || parts[0] != wantUser || parts[1] != wantPass {
			http.Error(w, "bad creds", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"token":"registry-jwt"}`))
	}))
	t.Cleanup(srv.Close)

	host := srv.URL[len("http://"):]
	repo := imageRepo{Registry: host, Name: "acme/widget"}
	tok, err := registryBearerToken(testContext(t), srv.Client(), repo, wantPass)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "registry-jwt" {
		t.Fatalf("token=%q", tok)
	}
	if gotAuth == "" {
		t.Fatal("missing Authorization")
	}
}
