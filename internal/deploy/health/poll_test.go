package health

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/deploy/compose"
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
	if err := Poll(ctx, srv.Client(), srv.URL, time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func TestPollZeroIntervalUsesDefault(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "starting", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	// The budget ends well before DefaultPollInterval, so a zero interval that
	// resolves to the default leaves time for one round only.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := Poll(ctx, srv.Client(), srv.URL, 0); derrors.CodeOf(err) != derrors.CodeTimeout {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("health calls = %d, want 1 (one round, then the default pause)", got)
	}
}

func TestPollTimeoutKeepsLastError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "key required", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := Poll(ctx, srv.Client(), srv.URL, 0)
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
	err := Poll(ctx, &http.Client{}, url, time.Millisecond)
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
	err := Poll(ctx, srv.Client(), srv.URL, 0)
	if derrors.CodeOf(err) != derrors.CodeCanceled {
		t.Fatalf("err = %v, want canceled", err)
	}
}

func TestPollNeedsDeadline(t *testing.T) {
	if err := Poll(context.Background(), &http.Client{}, "http://127.0.0.1:1", 0); derrors.CodeOf(err) != derrors.CodeFailedPrecondition {
		t.Fatalf("err = %v, want a missing deadline refused", err)
	}
}

func TestDumpGatewayRefusesANilContext(t *testing.T) {
	//nolint:staticcheck // SA1012: the nil context is the case under test.
	if err := DumpGateway(nil, io.Discard, compose.Client{}); derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("err = %v, want a nil context refused", err)
	}
}
