package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// TestManifestExistsFollowsTheChallengeRealm: a registry whose token service lives
// on another host (Docker Hub's auth.docker.io) names it in WWW-Authenticate; the
// client asks that realm, with the challenge's service and scope.
func TestManifestExistsFollowsTheChallengeRealm(t *testing.T) {
	log := &authLog{}
	var gotService, gotScope string
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.add(r.Header.Get("Authorization"))
		gotService, gotScope = r.URL.Query().Get("service"), r.URL.Query().Get("scope")
		_, _ = w.Write([]byte(`{"access_token":"realm-jwt"}`))
	}))
	t.Cleanup(auth.Close)
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.add(r.Header.Get("Authorization"))
		if r.URL.Path == "/token" {
			t.Error("the registry's own /token must not be asked when the challenge names a realm")
		}
		if r.Header.Get("Authorization") == "Bearer realm-jwt" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="`+auth.URL+`/auth",service="registry.test",scope="repository:acme/widget:pull"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(reg.Close)

	c := Config{GitHubToken: secretPAT}.CreateClient()
	if err := c.ManifestExists(testContext(t), refOn(t, reg)); err != nil {
		t.Fatal(err)
	}
	if gotService != "registry.test" || gotScope != "repository:acme/widget:pull" {
		t.Fatalf("token request service=%q scope=%q", gotService, gotScope)
	}
	log.leaked(t)
}

// TestGHCRTokenGoesOnlyToGHCRRealm: a ghcr.io challenge that names another realm
// gets an anonymous token request; the GitHub token stays on ghcr.io.
func TestGHCRTokenGoesOnlyToGHCRRealm(t *testing.T) {
	var tokenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			tokenAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"token":"anon"}`))
			return
		}
		if r.Header.Get("Authorization") == "Bearer anon" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="https://auth.example.test/elsewhere",service="x"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	ref := Ref{Registry: GHCRHost, Repository: "acme/widget", Tag: "1.2.3"}
	if err := ghcrClient(t, srv, secretPAT).ManifestExists(testContext(t), ref); err != nil {
		t.Fatal(err)
	}
	if tokenAuth != "" {
		t.Fatalf("a non-ghcr.io realm got credentials: %q", tokenAuth)
	}
}

// TestTokenEndpointOutageIsNotARefusedToken: a token endpoint that does not answer
// before the deadline keeps its own code, so an operator is not told to rotate a
// working token during a registry outage.
func TestTokenEndpointOutageIsNotARefusedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := Config{}.CreateClient().ManifestExists(ctx, refOn(t, srv))
	if err == nil || errors.Is(err, ErrUnauthorized) || derrors.CodeOf(err) == derrors.CodeUnauthorized {
		t.Fatalf("err = %v, want the outage's own code, not unauthorized", err)
	}
	if strings.Contains(err.Error(), "read:packages") {
		t.Fatalf("err = %v must not blame the GitHub token", err)
	}
}

func TestTokenStatusCode(t *testing.T) {
	for status, want := range map[int]derrors.Code{
		http.StatusUnauthorized:       derrors.CodeUnauthorized,
		http.StatusForbidden:          derrors.CodeUnauthorized,
		http.StatusNotFound:           derrors.CodeUnauthorized,
		http.StatusTooManyRequests:    derrors.CodeRateLimited,
		http.StatusServiceUnavailable: derrors.CodeUnavailable,
	} {
		if got := tokenStatusCode(status); got != want {
			t.Fatalf("status %d: code %s, want %s", status, got, want)
		}
	}
}

func TestParseBearerChallenge(t *testing.T) {
	got := parseBearerChallenge(`Bearer realm="https://auth.docker.io/token", service="registry.docker.io",scope="repository:a/b:pull,push"`)
	if got["realm"] != "https://auth.docker.io/token" || got["service"] != "registry.docker.io" || got["scope"] != "repository:a/b:pull,push" {
		t.Fatalf("params %v", got)
	}
	if got := parseBearerChallenge(`Basic realm="x"`); got != nil {
		t.Fatalf("a Basic challenge has no bearer params, got %v", got)
	}
}

func TestTokenEndpointRefusesAPlainHTTPRealm(t *testing.T) {
	ref := Ref{Registry: "registry.example.test", Repository: "a/b", Tag: "1"}
	if _, err := tokenEndpoint(ref, `Bearer realm="http://auth.example.test/token"`); derrors.CodeOf(err) != derrors.CodeUnauthorized {
		t.Fatalf("err = %v, want a plain-http remote realm refused", err)
	}
	u, err := tokenEndpoint(ref, "")
	if err != nil || u.String() != "https://registry.example.test/token?scope=repository%3Aa%2Fb%3Apull&service=registry.example.test" {
		t.Fatalf("fallback = %v, %v", u, err)
	}
}
