package registry

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

const secretPAT = "ghp_never_leave_ghcr"

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// authLog records every Authorization header a test registry receives.
type authLog struct {
	mu      sync.Mutex
	headers []string
}

func (l *authLog) add(h string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.headers = append(l.headers, h)
}

func (l *authLog) leaked(t *testing.T) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	basic := base64.StdEncoding.EncodeToString([]byte(defaultBasicUser + ":" + secretPAT))
	for _, h := range l.headers {
		if strings.Contains(h, secretPAT) || strings.Contains(h, basic) {
			t.Fatalf("the GitHub token reached a non-ghcr.io registry: Authorization %q", h)
		}
	}
}

// privateRegistry answers 401 to a manifest GET unless it carries its own anonymous
// pull token, the way registries gate public images behind a token exchange.
func privateRegistry(t *testing.T, log *authLog, tokenStatus int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.add(r.Header.Get("Authorization"))
		switch {
		case r.URL.Path == "/token":
			if tokenStatus != http.StatusOK {
				w.WriteHeader(tokenStatus)
				return
			}
			_, _ = w.Write([]byte(`{"token":"anon-jwt"}`))
		case r.Header.Get("Authorization") == "Bearer anon-jwt":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func refOn(t *testing.T, srv *httptest.Server) Ref {
	t.Helper()
	ref, err := ParseRef(strings.TrimPrefix(srv.URL, "http://") + "/acme/widget:1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestManifestExistsNeverSendsGitHubTokenToOtherHosts(t *testing.T) {
	log := &authLog{}
	srv := privateRegistry(t, log, http.StatusOK)
	c := Config{GitHubToken: secretPAT}.CreateClient()
	if err := c.ManifestExists(testContext(t), refOn(t, srv)); err != nil {
		t.Fatal(err)
	}
	log.leaked(t)
	if len(log.headers) != 3 {
		t.Fatalf("requests %d, want manifest, token and manifest again", len(log.headers))
	}
}

func TestManifestExistsTokenRefusedStillNoLeak(t *testing.T) {
	log := &authLog{}
	srv := privateRegistry(t, log, http.StatusUnauthorized)
	c := Config{GitHubToken: secretPAT}.CreateClient()
	err := c.ManifestExists(testContext(t), refOn(t, srv))
	if !errors.Is(err, ErrUnauthorized) || derrors.CodeOf(err) != derrors.CodeUnauthorized {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	log.leaked(t)
	for _, h := range log.headers {
		if h != "" {
			t.Fatalf("a non-ghcr.io registry got credentials: %q", h)
		}
	}
}

// ghcrTransport sends ghcr.io requests to a test server, so the ghcr.io path runs
// without the network.
type ghcrTransport struct{ target *url.URL }

func (g ghcrTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.URL.Scheme = g.target.Scheme
	clone.URL.Host = g.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func ghcrClient(t *testing.T, srv *httptest.Server, token string) *Client {
	t.Helper()
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return Config{HTTP: &http.Client{Transport: ghcrTransport{target: target}}, GitHubToken: token}.CreateClient()
}

func TestManifestExistsSendsGitHubTokenToGHCR(t *testing.T) {
	var tokenAuth, manifestAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"token":"ghcr-jwt"}`))
			return
		}
		manifestAuth = r.Header.Get("Authorization")
		if manifestAuth != "Bearer ghcr-jwt" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	ref := Ref{Registry: GHCRHost, Repository: "acme/widget", Tag: "1.2.3"}
	if err := ghcrClient(t, srv, secretPAT).ManifestExists(testContext(t), ref); err != nil {
		t.Fatal(err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(defaultBasicUser+":"+secretPAT))
	if tokenAuth != want {
		t.Fatalf("token exchange Authorization %q, want docker-login Basic auth", tokenAuth)
	}
}

func TestManifestExistsGHCRNeverSendsPATAsBearer(t *testing.T) {
	var auths authLog
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths.add(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	ref := Ref{Registry: GHCRHost, Repository: "acme/widget", Tag: "1.2.3"}
	err := ghcrClient(t, srv, secretPAT).ManifestExists(testContext(t), ref)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	for _, h := range auths.headers {
		if h == "Bearer "+secretPAT {
			t.Fatal("the PAT was sent as a Bearer token after the token exchange failed")
		}
	}
}

func TestManifestExistsStatuses(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   derrors.Code
	}{
		{http.StatusNotFound, derrors.CodeNotFound},
		{http.StatusTeapot, derrors.CodeUnavailable},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
		}))
		err := Config{}.CreateClient().ManifestExists(testContext(t), refOn(t, srv))
		srv.Close()
		if derrors.CodeOf(err) != tc.want {
			t.Fatalf("status %d: err %v, want code %s", tc.status, err, tc.want)
		}
	}
}

func TestManifestExistsNeedsDeadline(t *testing.T) {
	err := Config{}.CreateClient().ManifestExists(context.Background(), Ref{Registry: GHCRHost, Repository: "a/b", Tag: "1"})
	if derrors.CodeOf(err) != derrors.CodeFailedPrecondition {
		t.Fatalf("err = %v, want a missing deadline refused", err)
	}
}

func TestParseRef(t *testing.T) {
	ref, err := ParseRef("ghcr.io/behaviorengineering/polypus:1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Registry != GHCRHost || ref.Repository != "behaviorengineering/polypus" || ref.Tag != "1.2.3" {
		t.Fatalf("ref %+v", ref)
	}
	for _, bad := range []string{"", "ghcr.io/a/b@sha256:x", "ghcr.io/a/b", "noslash:1"} {
		if _, err := ParseRef(bad); err == nil {
			t.Fatalf("%q: want an error", bad)
		}
	}
}
