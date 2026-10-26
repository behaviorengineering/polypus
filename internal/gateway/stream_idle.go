package gateway

import (
	"context"
	"io"
	"time"
)

// idleTimeoutBody cancels the upstream request when no bytes arrive for idle. Each
// Read that returns data resets the timer, so a stream that keeps sending (Anthropic
// pings included) lives as long as it needs; one that stalls is cut off and the
// error counts against the backend.
type idleTimeoutBody struct {
	io.ReadCloser
	idle  time.Duration
	timer *time.Timer
}

func newIdleTimeoutBody(body io.ReadCloser, idle time.Duration, cancel context.CancelFunc) io.ReadCloser {
	if idle <= 0 || cancel == nil {
		return body
	}
	return &idleTimeoutBody{ReadCloser: body, idle: idle, timer: time.AfterFunc(idle, cancel)}
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.timer.Reset(b.idle)
	}
	return n, err
}

func (b *idleTimeoutBody) Close() error {
	b.timer.Stop()
	return b.ReadCloser.Close()
}
