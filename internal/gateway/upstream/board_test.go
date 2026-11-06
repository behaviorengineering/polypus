package upstream

import (
	"errors"
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestBoardNilPassthrough(t *testing.T) {
	var b *Board
	called := false
	err := b.Execute("x", func() error {
		called = true
		return nil
	})
	if err != nil || !called {
		t.Fatalf("nil board: err=%v called=%v", err, called)
	}
}

func TestBoardTripsAfterConsecutiveFailures(t *testing.T) {
	b := NewBoard()
	fail := errors.New("dial failed")
	for i := uint(0); i < ConsecutiveFailuresTrip; i++ {
		called := false
		err := b.Execute("leaf", func() error {
			called = true
			return fail
		})
		if !called || !errors.Is(err, fail) {
			t.Fatalf("attempt %d must still dial before the trip: called=%v err=%v", i, called, err)
		}
	}
	err := b.Execute("leaf", func() error {
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
	fail := errors.New("dial failed")
	for i := uint(0); i < ConsecutiveFailuresTrip; i++ {
		_ = b.Execute("leaf", func() error { return fail })
	}
	time.Sleep(2 * delay)

	started := make(chan struct{})
	release := make(chan struct{})
	trial := make(chan error, 1)
	go func() {
		trial <- b.Execute("leaf", func() error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	err := b.Execute("leaf", func() error {
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
	if err := b.Execute("leaf", func() error { return nil }); err != nil {
		t.Fatalf("a successful trial must close the breaker: %v", err)
	}
}

func TestBoardRateLimitedDoesNotTrip(t *testing.T) {
	b := NewBoard()
	fail := derrors.New(derrors.CodeRateLimited, "router.bifrost", "provider rate limited")
	for i := 0; i < 5; i++ {
		err := b.Execute("cf_local", func() error { return fail })
		if !errors.Is(err, derrors.ErrRateLimited) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	called := false
	if err := b.Execute("cf_local", func() error {
		called = true
		return nil
	}); err != nil || !called {
		t.Fatalf("breaker must stay closed: err=%v called=%v", err, called)
	}
}

func TestBoardSuccessResetsConsecutiveFailures(t *testing.T) {
	b := NewBoard()
	fail := errors.New("dial failed")
	_ = b.Execute("leaf", func() error { return fail })
	_ = b.Execute("leaf", func() error { return fail })
	if err := b.Execute("leaf", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := b.Execute("leaf", func() error { return fail }); !errors.Is(err, fail) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	err := b.Execute("leaf", func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
}

func TestStatusFailure(t *testing.T) {
	if err := StatusFailure(200); err != nil {
		t.Fatalf("200: %v", err)
	}
	if err := StatusFailure(404); err != nil {
		t.Fatalf("404: %v", err)
	}
	err := StatusFailure(502)
	if !ResponseWritten(err) {
		t.Fatalf("502: %v", err)
	}
}

func TestBoardCallerVisibleErrorsDoNotTrip(t *testing.T) {
	b := NewBoard()
	for _, code := range []derrors.Code{derrors.CodeRateLimited, derrors.CodeCanceled, derrors.CodeInvalid, derrors.CodeUnauthorized, derrors.CodeNotFound, derrors.CodeConflict} {
		fail := derrors.New(code, "router.anthropic", "caller error")
		for i := uint(0); i < ConsecutiveFailuresTrip+1; i++ {
			_ = b.Execute("claude_team", func() error { return fail })
		}
	}
	called := false
	if err := b.Execute("claude_team", func() error {
		called = true
		return nil
	}); err != nil || !called {
		t.Fatalf("caller errors must not open the breaker: err=%v called=%v", err, called)
	}
}

func TestBoardNotReadyTrips(t *testing.T) {
	b := NewBoard()
	fail := derrors.New(derrors.CodeNotReady, "router.anthropic", "out of credit")
	for i := uint(0); i < ConsecutiveFailuresTrip; i++ {
		_ = b.Execute("claude_team", func() error { return fail })
	}
	err := b.Execute("claude_team", func() error { return nil })
	if !Unavailable(err) {
		t.Fatalf("out-of-credit refusals must open the breaker, got %v", err)
	}
}

func TestZeroValueBoardWorks(t *testing.T) {
	var b Board
	if err := b.Execute("leaf", func() error { return nil }); err != nil {
		t.Fatalf("a zero Board must run fn, not panic on a nil map: %v", err)
	}
	if snap := b.Snapshot(); len(snap) != 1 || snap[0].Name != "leaf" {
		t.Fatalf("snapshot=%+v", snap)
	}
}
