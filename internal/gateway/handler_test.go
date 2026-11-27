package gateway

import (
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// testNow is the frozen job clock gateway tests inject so cache TTLs do not wait
// on wall time.
func testNow() time.Time {
	return time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
}

func withTestClock() HandlerOption {
	return WithClock(testNow)
}

// newHandlerForTest builds a handler with t.Context() and a frozen clock.
func newHandlerForTest(t *testing.T, opts config.ServeOptions, extra ...HandlerOption) (Handler, error) {
	t.Helper()
	extra = append([]HandlerOption{withTestClock()}, extra...)
	return NewHandler(t.Context(), opts, extra...)
}

func TestNewHandlerNilContext(t *testing.T) {
	//nolint:staticcheck // SA1012: a nil context is the case under test.
	_, err := NewHandler(nil, config.ServeOptions{}, withTestClock())
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("NewHandler(nil): want CodeInvalid, got %v", err)
	}
}

func TestNewHandlerRequiresClock(t *testing.T) {
	_, err := NewHandler(t.Context(), config.ServeOptions{})
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("NewHandler without clock: want CodeInvalid, got %v", err)
	}
}
