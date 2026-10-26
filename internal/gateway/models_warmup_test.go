package gateway

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestModelsRefreshLoopCancels(t *testing.T) {
	tick := make(chan time.Time)
	factory := func(time.Duration) (<-chan time.Time, func()) {
		return tick, func() {}
	}
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runModelsRefreshLoop(ctx, time.Minute, factory, func(context.Context) {
			calls.Add(1)
		})
		close(done)
	}()
	tick <- time.Now()
	tick <- time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not exit after cancel")
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 warm calls, got %d", calls.Load())
	}
}
