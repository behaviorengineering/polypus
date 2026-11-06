package github

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// breakerThreshold is how many failures in a row open an outbound breaker.
const breakerThreshold = 5

// tripGitHubBreaker sends single-attempt 503s through p's GitHub breaker until it
// opens. Each Client call retries with seconds of backoff, so tripping through the
// client would make the test slow; the breaker is the same one the client uses.
func tripGitHubBreaker(t *testing.T, p *outbound.Policies, url string) {
	t.Helper()
	for range breakerThreshold {
		req, err := http.NewRequestWithContext(testContext(t), http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := p.DoOnce(testContext(t), outbound.DepGitHub, http.DefaultClient, req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
}

// countingServer answers status and counts the requests that reach it.
func countingServer(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func newClientWith(t *testing.T, srv *httptest.Server, p *outbound.Policies) *Client {
	t.Helper()
	c, err := Config{HTTP: srv.Client(), Token: "test", BaseURL: srv.URL, OwnerRepo: "acme/polypus", Policies: p}.CreateClient()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClientBreakerOpensAfterFailures(t *testing.T) {
	srv, hits := countingServer(t, http.StatusServiceUnavailable, "down")
	p := outbound.NewPolicies()
	tripGitHubBreaker(t, p, srv.URL)
	before := hits.Load()

	_, err := newClientWith(t, srv, p).TagCommit(testContext(t), "v1.2.3")
	if derrors.CodeOf(err) != derrors.CodeUnavailable {
		t.Fatalf("err = %v, want CodeUnavailable from the open breaker", err)
	}
	if hits.Load() != before {
		t.Fatalf("an open breaker must fail fast without reaching GitHub; %d more requests", hits.Load()-before)
	}
}

func TestClientsDoNotShareBreakers(t *testing.T) {
	down, _ := countingServer(t, http.StatusServiceUnavailable, "down")
	tripped := outbound.NewPolicies()
	tripGitHubBreaker(t, tripped, down.URL)

	up, hits := countingServer(t, http.StatusOK, `{"object":{"type":"commit","sha":"commitsha"}}`)
	ctx := testContext(t)
	sha, err := newClientWith(t, up, outbound.NewPolicies()).TagCommit(ctx, "v1.2.3")
	if err != nil || sha != "commitsha" {
		t.Fatalf("a client with fresh policies must not see another's open breaker: sha %q err %v", sha, err)
	}
	if hits.Load() != 1 {
		t.Fatalf("requests = %d, want 1", hits.Load())
	}
	if _, err := newClientWith(t, up, tripped).TagCommit(ctx, "v1.2.3"); derrors.CodeOf(err) != derrors.CodeUnavailable {
		t.Fatalf("the tripped policies still hold their open breaker: %v", err)
	}
}
