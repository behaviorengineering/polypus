package registry

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// manifestServer answers every manifest GET with status and counts the requests.
func manifestServer(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// hostOf is the host:port a registry ref names for srv.
func hostOf(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

// ghcrTo sends requests for ghcr.io to srv over plain HTTP, and every other
// host where it names.
type ghcrTo struct{ host string }

func (g ghcrTo) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == GHCRHost {
		r = r.Clone(r.Context())
		r.URL.Scheme = "http"
		r.URL.Host = g.host
	}
	return http.DefaultTransport.RoundTrip(r)
}

func TestManifestBreakerScopedPerHost(t *testing.T) {
	down, downHits := manifestServer(t, http.StatusServiceUnavailable)
	up, upHits := manifestServer(t, http.StatusOK)
	downHost, upHost := hostOf(t, down), hostOf(t, up)

	// Trip the breaker for the failing host with single attempts: tripping it
	// through ManifestExists would pay seconds of retry backoff per call.
	p := outbound.NewPolicies()
	for range 5 {
		req, err := http.NewRequestWithContext(testContext(t), http.MethodGet, down.URL+"/v2/", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := p.DoOnce(testContext(t), outbound.Scoped(outbound.DepRegistry, downHost), http.DefaultClient, req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	before := downHits.Load()

	c := Config{HTTP: &http.Client{Transport: ghcrTo{host: upHost}}, Policies: p}.CreateClient()
	err := c.ManifestExists(testContext(t), Ref{Registry: downHost, Repository: "acme/polypus", Tag: "1.0.0"})
	if derrors.CodeOf(err) != derrors.CodeUnavailable {
		t.Fatalf("tripped host: err = %v, want CodeUnavailable", err)
	}
	if downHits.Load() != before {
		t.Fatal("the open breaker must fail fast without reaching the tripped host")
	}
	for _, ref := range []Ref{
		{Registry: upHost, Repository: "acme/polypus", Tag: "1.0.0"},
		{Registry: GHCRHost, Repository: "acme/polypus", Tag: "1.0.0"},
	} {
		if err := c.ManifestExists(testContext(t), ref); err != nil {
			t.Fatalf("%s: one host's open breaker must not block another: %v", ref.Registry, err)
		}
	}
	if upHits.Load() != 2 {
		t.Fatalf("healthy host and ghcr.io requests = %d, want 2", upHits.Load())
	}
}
