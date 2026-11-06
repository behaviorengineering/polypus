package cli

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServeWarnsWhenKeyless(t *testing.T) {
	t.Setenv("POLYPUS_ADMIN_KEYS", filepath.Join(t.TempDir(), "admin-api-keys.json"))
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	if err := warnIfKeyless(logger); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.Contains(got, "no access keys") || !strings.Contains(got, keylessHint) {
		t.Fatalf("keyless gateway: want the warning with its hint, got %q", got)
	}

	store, err := openAdminKeyStore()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := store.Generate(ctx, "ci"); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := warnIfKeyless(logger); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "" {
		t.Fatalf("locked gateway: want no warning, got %q", got)
	}
}
