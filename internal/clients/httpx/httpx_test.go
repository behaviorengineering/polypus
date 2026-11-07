package httpx

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

type modelList struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
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
	h := HTTP{Client: srv.Client(), Policies: outbound.NewPolicies(), Dep: "test"}
	list, err := GetJSON[modelList](getJSONCtx(t), h, JSONGet{Op: "test.get", URL: srv.URL, Header: http.Header{"X-Key": {"k"}}, MaxBody: 1 << 10})
	if err != nil {
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
		h := HTTP{Client: srv.Client(), Policies: outbound.NewPolicies(), Dep: "test"}
		_, err := GetJSON[modelList](getJSONCtx(t), h, JSONGet{Op: "test.get", URL: srv.URL, MaxBody: 1 << 10})
		srv.Close()
		if got := derrors.CodeOf(err); got != tc.want {
			t.Fatalf("status %d: code %q, want %q (err %v)", tc.status, got, tc.want, err)
		}
	}
}

func TestStatusErrorCodesWithoutARetryMark(t *testing.T) {
	if err := statusError("op", http.StatusOK, nil); err != nil {
		t.Fatalf("2xx must map to nil, got %v", err)
	}
	if got := derrors.CodeOf(statusError("op", http.StatusTooManyRequests, nil)); got != derrors.CodeRateLimited {
		t.Fatalf("429 code = %q", got)
	}
	if got := derrors.CodeOf(statusError("op", http.StatusBadGateway, []byte("down"))); got != derrors.CodeUnavailable {
		t.Fatalf("502 code = %q", got)
	}
	// The policies have already finished when statusError runs, so it marks nothing
	// for them: the text stays the upstream status alone.
	err := statusError("op", http.StatusNotFound, []byte("gone"))
	if errors.Is(err, outbound.ErrAbort) || strings.Contains(err.Error(), "non-retryable") {
		t.Fatalf("a 4xx must not carry an outbound abort mark, got %v", err)
	}
	if err.Error() != "op: status 404: gone" {
		t.Fatalf("err text = %q", err)
	}
}

func TestGetJSONRejectsOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[` + strings.Repeat(" ", 64) + `]}`))
	}))
	t.Cleanup(srv.Close)
	h := HTTP{Client: srv.Client(), Policies: outbound.NewPolicies(), Dep: "test"}
	_, err := GetJSON[modelList](getJSONCtx(t), h, JSONGet{Op: "test.get", URL: srv.URL, MaxBody: 16})
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

func TestTruncate(t *testing.T) {
	if got := Truncate("abcdef", 3); got != "abc..." {
		t.Fatalf("Truncate cut = %q", got)
	}
	if got := Truncate("  ab  ", 3); got != "ab" {
		t.Fatalf("Truncate short = %q", got)
	}
	if got := Truncate("héllo wörld", 2); got != "hé..." {
		t.Fatalf("Truncate must cut on runes, got %q", got)
	}
}
