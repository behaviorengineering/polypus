package catalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

func getJSONCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestGetJSONDecodesAndSendsHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Key") != "k" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m"}]}`))
	}))
	t.Cleanup(srv.Close)
	var list ModelList
	h := HTTP{Client: srv.Client(), Policies: outbound.NewPolicies(), Dep: "test"}
	if err := GetJSON(getJSONCtx(t), h, "test.get", srv.URL, http.Header{"X-Key": {"k"}}, 1<<10, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 || list.Data[0].ID != "m" {
		t.Fatalf("list = %+v", list)
	}
}

func TestGetJSONMapsUpstreamStatus(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   derrors.Code
	}{
		{http.StatusUnauthorized, derrors.CodeUnauthorized},
		{http.StatusForbidden, derrors.CodeUnauthorized},
		{http.StatusNotFound, derrors.CodeNotFound},
		{http.StatusBadRequest, derrors.CodeInvalid},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte("nope"))
		}))
		var list ModelList
		h := HTTP{Client: srv.Client(), Policies: outbound.NewPolicies(), Dep: "test"}
		err := GetJSON(getJSONCtx(t), h, "test.get", srv.URL, nil, 1<<10, &list)
		srv.Close()
		if got := derrors.CodeOf(err); got != tc.want {
			t.Fatalf("status %d: code %q, want %q (err %v)", tc.status, got, tc.want, err)
		}
	}
}

func TestStatusErrorRetryableStatuses(t *testing.T) {
	if err := StatusError("op", http.StatusOK, nil); err != nil {
		t.Fatalf("2xx must map to nil, got %v", err)
	}
	if got := derrors.CodeOf(StatusError("op", http.StatusTooManyRequests, nil)); got != derrors.CodeRateLimited {
		t.Fatalf("429 code = %q", got)
	}
	err := StatusError("op", http.StatusBadGateway, []byte("down"))
	if got := derrors.CodeOf(err); got != derrors.CodeUnavailable {
		t.Fatalf("502 code = %q", got)
	}
	if errors.Is(err, outbound.ErrAbort) {
		t.Fatal("a 5xx must stay retryable")
	}
	if !errors.Is(StatusError("op", http.StatusNotFound, nil), outbound.ErrAbort) {
		t.Fatal("a permanent 4xx must be marked non-retryable")
	}
}

func TestGetJSONRejectsOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[` + strings.Repeat(" ", 64) + `]}`))
	}))
	t.Cleanup(srv.Close)
	var list ModelList
	h := HTTP{Client: srv.Client(), Policies: outbound.NewPolicies(), Dep: "test"}
	err := GetJSON(getJSONCtx(t), h, "test.get", srv.URL, nil, 16, &list)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("want a too-large error, got %v", err)
	}
}

func TestReadCappedRejectsOversizedBodies(t *testing.T) {
	if raw, err := ReadCapped(strings.NewReader("12345"), 5, "test"); err != nil || string(raw) != "12345" {
		t.Fatalf("a body at the limit must pass: %q %v", raw, err)
	}
	_, err := ReadCapped(strings.NewReader("123456"), 5, "test")
	if err == nil {
		t.Fatal("a body past the limit must fail, not truncate")
	}
	if !errors.Is(err, derrors.ErrUnavailable) {
		t.Fatalf("a too-large body is CodeUnavailable, got %v", err)
	}
}
