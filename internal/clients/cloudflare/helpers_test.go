package cloudflare

import (
	"context"
	"testing"
	"time"
)

// hopCtx is a caller context with the deadline the outbound seam requires.
func hopCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}
