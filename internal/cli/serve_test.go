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
	keysPath := filepath.Join(t.TempDir(), "admin-api-keys.json")
	t.Setenv("POLYPUS_ADMIN_KEYS", keysPath)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	path, err := warnIfKeyless(logger)
	if err != nil {
		t.Fatal(err)
	}
	if path != keysPath {
		t.Fatalf("store path %q, want %q", path, keysPath)
	}
	if got := buf.String(); !strings.Contains(got, "no access keys") || !strings.Contains(got, keylessHint) || !strings.Contains(got, keysPath) {
		t.Fatalf("keyless gateway: want the warning with its hint and store path, got %q", got)
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
	if _, err := warnIfKeyless(logger); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "" {
		t.Fatalf("locked gateway: want no warning, got %q", got)
	}
}

func TestServeKeyStoreFailureNamesThePath(t *testing.T) {
	// A directory where the store file should be cannot be read as a key store.
	dir := t.TempDir()
	t.Setenv("POLYPUS_ADMIN_KEYS", dir)
	path, err := warnIfKeyless(slog.New(slog.DiscardHandler))
	if err == nil {
		t.Fatal("a directory is not a key store; want an error")
	}
	if path != dir {
		t.Fatalf("failure path %q, want %q", path, dir)
	}
}

func TestServeBackendFlagOverridesEnv(t *testing.T) {
	for _, k := range []string{"POLYPUS_BACKEND_URL", "POLYPUS_MLX_URL", "POLYPUS_MLX_HOST", "POLYPUS_MLX_PORT"} {
		t.Setenv(k, "")
	}
	opts, err := resolveServeOptions(serveOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if opts.BackendURL != "" {
		t.Fatalf("no flag and no MLX env: BackendURL %q, want empty so the config's mlx_local stands", opts.BackendURL)
	}
	opts, err = resolveServeOptions(serveOpts{host: "0.0.0.0", port: 1400, backend: "http://10.0.0.5:1322"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Host != "0.0.0.0" || opts.Port != 1400 || opts.BackendURL != "http://10.0.0.5:1322" {
		t.Fatalf("flags not applied: %+v", opts)
	}
}
