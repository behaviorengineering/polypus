package openaicompat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

func testClient(srv *httptest.Server) Client {
	return Client{HTTP: srv.Client(), Policies: outbound.NewPolicies()}
}

func bounded(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestListModelsSendsAuthAndParsesTheList(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m1","created":7,"owned_by":"x"},{"id":"  "}]}`))
	}))
	t.Cleanup(srv.Close)
	models, err := testClient(srv).ListModels(bounded(t), srv.URL+"/v1/models", "Bearer k")
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer k" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if len(models) != 1 || models[0] != (Model{ID: "m1", Created: 7, OwnedBy: "x"}) {
		t.Fatalf("entries without an id are dropped: %+v", models)
	}
}

func TestParseModelsAcceptsABareArray(t *testing.T) {
	if got := ParseModels([]byte(`[{"id":"a"},{"id":"b"}]`)); len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got := ParseModels([]byte(`not json`)); got != nil {
		t.Fatalf("an unreadable body has no models: %+v", got)
	}
}

func TestListModelsFailsOnANon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	if _, err := testClient(srv).ListModels(bounded(t), srv.URL, ""); derrors.CodeOf(err) != derrors.CodeUnavailable {
		t.Fatalf("a 401 list must fail as unavailable: %v", err)
	}
}

func TestPingPassesBelow500AndCheckHealthNeeds2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	c := testClient(srv)
	if err := c.Ping(bounded(t), srv.URL+"/"); err != nil {
		t.Fatalf("a 404 still shows the backend is up: %v", err)
	}
	if err := c.CheckHealth(bounded(t), srv.URL+"/health"); derrors.CodeOf(err) != derrors.CodeUnavailable {
		t.Fatalf("a 404 health endpoint is unhealthy: %v", err)
	}
}

func TestCallsFailClosedWithoutADeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request may leave without a deadline")
	}))
	t.Cleanup(srv.Close)
	if err := testClient(srv).Ping(context.Background(), srv.URL); err == nil {
		t.Fatal("a ping without a deadline must fail")
	}
}

func TestZeroClientIsRejected(t *testing.T) {
	if _, err := (Client{}).ListModels(context.Background(), "http://x", ""); derrors.CodeOf(err) != derrors.CodeInternal {
		t.Fatalf("a client without HTTP or policies must be rejected: %v", err)
	}
}
