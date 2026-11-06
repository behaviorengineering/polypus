package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestPollOKAfterTransient(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "reset", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Poll(ctx, srv.Client(), srv.URL); err != nil {
		t.Fatal(err)
	}
}

func TestPollTimeoutKeepsLastError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "key required", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := Poll(ctx, srv.Client(), srv.URL)
	if derrors.CodeOf(err) != derrors.CodeTimeout {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("err = %v, want the last failure (401) kept", err)
	}
}

func TestPollTransportErrorKept(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := Poll(ctx, &http.Client{}, url)
	if err == nil || !strings.Contains(err.Error(), "GET /health") {
		t.Fatalf("err = %v, want the dial failure named", err)
	}
}

func TestPollCancelDuringWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	err := Poll(ctx, srv.Client(), srv.URL)
	if derrors.CodeOf(err) != derrors.CodeCanceled {
		t.Fatalf("err = %v, want canceled", err)
	}
}

func TestPollNeedsDeadline(t *testing.T) {
	if err := Poll(context.Background(), &http.Client{}, "http://127.0.0.1:1"); derrors.CodeOf(err) != derrors.CodeFailedPrecondition {
		t.Fatalf("err = %v, want a missing deadline refused", err)
	}
}
