package gateway

import (
	"context"
	"errors"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
)

func TestClientHangUpsDoNotOpenTheBreaker(t *testing.T) {
	board := upstream.NewBoard()
	hangUp := afterHeaders(context.Background(), errors.New("write: broken pipe"), true, "gateway.test", "write stream")
	if derrors.CodeOf(hangUp) != derrors.CodeCanceled || !upstream.ResponseWritten(hangUp) {
		t.Fatalf("a failed client write must be CodeCanceled and response-written: %v", hangUp)
	}
	for range upstream.ConsecutiveFailuresTrip + 2 {
		_ = board.Execute("claude_team", func() error { return hangUp })
	}
	ran := false
	if err := board.Execute("claude_team", func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("client hang-ups opened the breaker: err=%v ran=%v", err, ran)
	}
}

func TestUpstreamStreamFailureStillCounts(t *testing.T) {
	board := upstream.NewBoard()
	dead := afterHeaders(context.Background(), errors.New("unexpected EOF"), false, "gateway.test", "copy stream")
	if derrors.CodeOf(dead) != derrors.CodeUnavailable {
		t.Fatalf("an upstream read failure must be CodeUnavailable: %v", dead)
	}
	for range upstream.ConsecutiveFailuresTrip {
		_ = board.Execute("claude_team", func() error { return dead })
	}
	if err := board.Execute("claude_team", func() error { return nil }); !upstream.Unavailable(err) {
		t.Fatalf("a dead upstream must open the breaker, got %v", err)
	}
}

func TestCopyFailureAfterClientLeftIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := afterHeaders(ctx, errors.New("context canceled"), false, "gateway.test", "copy stream")
	if derrors.CodeOf(err) != derrors.CodeCanceled {
		t.Fatalf("a copy failure after the client left must be CodeCanceled: %v", err)
	}
}
