package upstream

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestBoardNilPassthrough(t *testing.T) {
	var b *Board
	called := false
	err := b.Execute(t.Context(), "x", func() error {
		called = true
		return nil
	})
	if err != nil || !called {
		t.Fatalf("nil board: err=%v called=%v", err, called)
	}
}

func TestBoardTripsAfterConsecutiveFailures(t *testing.T) {
	b := NewBoard()
	fail := derrors.New(derrors.CodeUnavailable, "router.bifrost", "dial failed")
	for i := uint(0); i < ConsecutiveFailuresTrip; i++ {
		called := false
		err := b.Execute(t.Context(), "leaf", func() error {
			called = true
			return fail
		})
		if !called || !errors.Is(err, fail) {
			t.Fatalf("attempt %d must still dial before the trip: called=%v err=%v", i, called, err)
		}
	}
	err := b.Execute(t.Context(), "leaf", func() error {
		t.Fatal("must not dial while open")
		return nil
	})
	if !Unavailable(err) {
		t.Fatalf("want open, got %v", err)
	}
	if !errors.Is(err, ErrOpen) || errors.Is(err, ErrHalfOpen) {
		t.Fatalf("want ErrOpen, got %v", err)
	}
	if derrors.CodeOf(err) != derrors.CodeUnavailable || derrors.Field(err, FieldUpstream) != "leaf" {
		t.Fatalf("refusal must be CodeUnavailable naming the upstream: %v", err)
	}
}

func TestBoardHalfOpenRefusalIsLabelledHalfOpen(t *testing.T) {
	const delay = 20 * time.Millisecond
	b := &Board{openDelay: delay}
	fail := derrors.New(derrors.CodeUnavailable, "router.bifrost", "dial failed")
	for i := uint(0); i < ConsecutiveFailuresTrip; i++ {
		_ = b.Execute(t.Context(), "leaf", func() error { return fail })
	}
	time.Sleep(2 * delay)

	started := make(chan struct{})
	release := make(chan struct{})
	trial := make(chan error, 1)
	go func() {
		trial <- b.Execute(t.Context(), "leaf", func() error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	err := b.Execute(t.Context(), "leaf", func() error {
		t.Error("must not dial while the trial call is in flight")
		return nil
	})
	close(release)
	if trialErr := <-trial; trialErr != nil {
		t.Fatalf("trial call: %v", trialErr)
	}
	if !Unavailable(err) || !errors.Is(err, ErrHalfOpen) {
		t.Fatalf("want a half-open refusal, got %v", err)
	}
	if cls := ClassifyDialFailure(err, ""); cls.BreakerState != stateHalfOpen || cls.Upstream != "leaf" {
		t.Fatalf("classification %+v", cls)
	}
	if err := b.Execute(t.Context(), "leaf", func() error { return nil }); err != nil {
		t.Fatalf("a successful trial must close the breaker: %v", err)
	}
}

func TestBoardRateLimitedDoesNotTrip(t *testing.T) {
	b := NewBoard()
	fail := derrors.New(derrors.CodeRateLimited, "router.bifrost", "provider rate limited")
	for i := 0; i < 5; i++ {
		err := b.Execute(t.Context(), "cf_local", func() error { return fail })
		if !errors.Is(err, derrors.ErrRateLimited) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	called := false
	if err := b.Execute(t.Context(), "cf_local", func() error {
		called = true
		return nil
	}); err != nil || !called {
		t.Fatalf("breaker must stay closed: err=%v called=%v", err, called)
	}
}

func TestBoardSuccessResetsConsecutiveFailures(t *testing.T) {
	b := NewBoard()
	fail := derrors.New(derrors.CodeUnavailable, "router.bifrost", "dial failed")
	_ = b.Execute(t.Context(), "leaf", func() error { return fail })
	_ = b.Execute(t.Context(), "leaf", func() error { return fail })
	if err := b.Execute(t.Context(), "leaf", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := b.Execute(t.Context(), "leaf", func() error { return fail }); !errors.Is(err, fail) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	err := b.Execute(t.Context(), "leaf", func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
}

func TestBoardCallerVisibleErrorsDoNotTrip(t *testing.T) {
	b := NewBoard()
	for _, code := range []derrors.Code{derrors.CodeRateLimited, derrors.CodeCanceled, derrors.CodeInvalid, derrors.CodeUnauthorized, derrors.CodeNotFound, derrors.CodeConflict} {
		fail := derrors.New(code, "router.anthropic", "caller error")
		for i := uint(0); i < ConsecutiveFailuresTrip+1; i++ {
			_ = b.Execute(t.Context(), "claude_team", func() error { return fail })
		}
	}
	called := false
	if err := b.Execute(t.Context(), "claude_team", func() error {
		called = true
		return nil
	}); err != nil || !called {
		t.Fatalf("caller errors must not open the breaker: err=%v called=%v", err, called)
	}
}

func TestBoardLocalErrorsDoNotTrip(t *testing.T) {
	b := NewBoard()
	for _, fail := range []error{
		derrors.New(derrors.CodeNotReady, "gateway.proxyCloudflareSystemOne", "cloudflare credential missing"),
		derrors.New(derrors.CodeNotReady, "router.anthropic", "out of credit"),
		derrors.New(derrors.CodeFailedPrecondition, "gateway.dialChat", "backend not registered"),
		derrors.New(derrors.CodeInternal, "gateway.rewrite", "marshal model"),
		derrors.New(derrors.CodeUnimplemented, "batches.Cancel", "provider cannot cancel"),
		errors.New("no domain code"),
	} {
		for i := uint(0); i < ConsecutiveFailuresTrip+1; i++ {
			if err := b.Execute(t.Context(), "claude_team", func() error { return fail }); !errors.Is(err, fail) {
				t.Fatalf("Execute must return fn's own error, got %v", err)
			}
		}
	}
	if err := b.Execute(t.Context(), "claude_team", func() error { return nil }); err != nil {
		t.Fatalf("errors raised inside Polypus must not open the breaker, got %v", err)
	}
}

func TestBoardUpstream4xxDoesNotTrip(t *testing.T) {
	b := NewBoard()
	// bifrostErr codes a provider answer it cannot classify as unavailable, with
	// the status alongside: a 4xx is still the backend answering.
	fail := derrors.New(derrors.CodeUnavailable, "router.bifrost", "model not found").With("status", "400")
	for i := uint(0); i < ConsecutiveFailuresTrip+1; i++ {
		_ = b.Execute(t.Context(), "leaf", func() error { return fail })
	}
	if err := b.Execute(t.Context(), "leaf", func() error { return nil }); err != nil {
		t.Fatalf("an upstream 4xx must not open the breaker, got %v", err)
	}
	b5 := NewBoard()
	fail5 := derrors.New(derrors.CodeUnavailable, "router.bifrost", "bad gateway").With("status", "502")
	for i := uint(0); i < ConsecutiveFailuresTrip; i++ {
		_ = b5.Execute(t.Context(), "leaf", func() error { return fail5 })
	}
	if err := b5.Execute(t.Context(), "leaf", func() error { return nil }); !Unavailable(err) {
		t.Fatalf("an upstream 5xx must open the breaker, got %v", err)
	}
}

func TestBoardTimeoutCountsOnlyOnTheGatewayBudget(t *testing.T) {
	timeout := derrors.New(derrors.CodeTimeout, "router.bifrost", "deadline exceeded")

	own := NewBoard()
	for i := uint(0); i < ConsecutiveFailuresTrip; i++ {
		_ = own.Execute(t.Context(), "leaf", func() error { return timeout })
	}
	if err := own.Execute(t.Context(), "leaf", func() error { return nil }); !Unavailable(err) {
		t.Fatalf("a backend that runs past the gateway's hop budget must open the breaker, got %v", err)
	}

	caller := NewBoard()
	ctx := config.WithCallerBudget(t.Context())
	for i := uint(0); i < ConsecutiveFailuresTrip+1; i++ {
		_ = caller.Execute(ctx, "leaf", func() error { return timeout })
	}
	if err := caller.Execute(ctx, "leaf", func() error { return nil }); err != nil {
		t.Fatalf("a deadline the caller chose must not open the breaker, got %v", err)
	}
}

func TestBackendFault(t *testing.T) {
	callerCtx := config.WithCallerBudget(context.Background())
	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{"nil", context.Background(), nil, false},
		{"unavailable", context.Background(), derrors.New(derrors.CodeUnavailable, "op", "dial"), true},
		{"unavailable 503", context.Background(), derrors.New(derrors.CodeUnavailable, "op", "x").With("status", "503"), true},
		{"unavailable 404", context.Background(), derrors.New(derrors.CodeUnavailable, "op", "x").With("status", "404"), false},
		{"own timeout", context.Background(), derrors.New(derrors.CodeTimeout, "op", "x"), true},
		{"caller timeout", callerCtx, derrors.New(derrors.CodeTimeout, "op", "x"), false},
		{"caller budget unavailable", callerCtx, derrors.New(derrors.CodeUnavailable, "op", "x"), true},
		{"rate limited", context.Background(), derrors.New(derrors.CodeRateLimited, "op", "x"), false},
		{"canceled", context.Background(), derrors.New(derrors.CodeCanceled, "op", "x"), false},
		{"not ready", context.Background(), derrors.New(derrors.CodeNotReady, "op", "x"), false},
		{"failed precondition", context.Background(), derrors.New(derrors.CodeFailedPrecondition, "op", "x"), false},
		{"internal", context.Background(), derrors.New(derrors.CodeInternal, "op", "x"), false},
		{"unimplemented", context.Background(), derrors.New(derrors.CodeUnimplemented, "op", "x"), false},
		{"invalid", context.Background(), derrors.New(derrors.CodeInvalid, "op", "x"), false},
		{"plain", context.Background(), errors.New("x"), false},
		{"wrapped written", context.Background(), fmt.Errorf("%w: %w", ErrResponseWritten, derrors.New(derrors.CodeUnavailable, "op", "x")), true},
	} {
		if got := backendFault(tc.ctx, tc.err); got != tc.want {
			t.Errorf("%s: backendFault=%v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestZeroValueBoardWorks(t *testing.T) {
	var b Board
	if err := b.Execute(t.Context(), "leaf", func() error { return nil }); err != nil {
		t.Fatalf("a zero Board must run fn, not panic on a nil map: %v", err)
	}
	if snap := b.Snapshot(); len(snap) != 1 || snap[0].Name != "leaf" {
		t.Fatalf("snapshot=%+v", snap)
	}
}
