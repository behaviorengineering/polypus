package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/admin/keys"
)

// adminKeyFixture points the CLI at an isolated key store holding one key, ops.
func adminKeyFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "admin-api-keys.json")
	t.Setenv("POLYPUS_ADMIN_KEYS", path)
	s, err := keys.Config{Path: path, Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }}.CreateStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Generate(context.Background(), "ops"); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAdminKeyListJSONHasNoHashes(t *testing.T) {
	adminKeyFixture(t)
	code, out, errOut := captureRun([]string{"admin-key", "list", "--json"})
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, errOut)
	}
	if strings.Contains(out, "hash") || strings.Contains(out, "argon2id") {
		t.Fatalf("list --json leaks stored hashes: %s", out)
	}
	var got adminKeyListJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("list --json does not parse: %v\n%s", err, out)
	}
	if len(got.Keys) != 1 || got.Keys[0].Name != "ops" || got.Keys[0].Prefix == "" {
		t.Fatalf("unexpected keys: %+v", got.Keys)
	}
}

// TestAdminKeyDryRunChecksWithoutPrompting covers the dry-run order: it never asks
// for confirmation (no --yes, no terminal), and it checks the store the way the
// write would.
func TestAdminKeyDryRunChecksWithoutPrompting(t *testing.T) {
	path := adminKeyFixture(t)
	before, err := os.ReadFile(path) //nolint:gosec // G304: test temp dir
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"rotate", "--name", "missing"}, 1, "no key named missing"},
		{[]string{"rotate", "--name", "ops"}, 0, "would rotate ops"},
		{[]string{"generate", "--name", "ops"}, 1, "name already exists"},
		{[]string{"generate", "--name", "ci"}, 0, "would create ci"},
		{[]string{"delete", "--name", "missing"}, 1, "no key named missing"},
		{[]string{"delete", "--name", "ops"}, 0, "would remove ops"},
	} {
		withStdin(t, "")
		args := append([]string{"admin-key"}, tc.args...)
		args = append(args, "--dry-run")
		code, _, errOut := captureRun(args)
		if code != tc.code || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: exit %d stderr %q, want exit %d and %q", args, code, errOut, tc.code, tc.want)
		}
		if strings.Contains(errOut, "not confirmed") || strings.Contains(errOut, "Confirm") {
			t.Errorf("%v: dry-run must not prompt: %q", args, errOut)
		}
	}
	after, err := os.ReadFile(path) //nolint:gosec // G304: test temp dir
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a dry-run changed the key store")
	}
}
