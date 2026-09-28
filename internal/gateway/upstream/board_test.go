package upstream

import (
	"errors"
	"testing"

	"github.com/sony/gobreaker"
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
	for i := 0; i < 3; i++ {
		err := b.Execute("leaf", func() error { return fail })
		if !errors.Is(err, fail) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	err := b.Execute("leaf", func() error {
		t.Fatal("must not dial while open")
		return nil
	})
	if !Unavailable(err) {
		t.Fatalf("want open, got %v", err)
	}
	if !errors.Is(err, gobreaker.ErrOpenState) {
		t.Fatalf("want ErrOpenState, got %v", err)
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
