package cloudflare

import (
	"context"
	"strings"
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

func TestReadCappedRejectsOversizedBodies(t *testing.T) {
	if raw, err := readCapped(strings.NewReader("12345"), 5, "test"); err != nil || string(raw) != "12345" {
		t.Fatalf("a body at the limit must pass: %q %v", raw, err)
	}
	if _, err := readCapped(strings.NewReader("123456"), 5, "test"); err == nil {
		t.Fatal("a body past the limit must fail, not truncate")
	}
}
