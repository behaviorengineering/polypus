package cli

import (
	"bytes"
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
		code, out, errOut := captureRun(args)
		// The plan is the result and goes to stdout; a refusal is a diagnostic.
		got := errOut
		if tc.code == 0 {
			got = out
		}
		if code != tc.code || !strings.Contains(got, tc.want) {
			t.Errorf("%v: exit %d stdout %q stderr %q, want exit %d and %q", args, code, out, errOut, tc.code, tc.want)
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

// promptIO is a cmdIO reading input as if typed at a terminal.
func promptIO(input string) (cmdIO, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return cmdIO{ctx: context.Background(), in: strings.NewReader(input), out: &out, errOut: &errOut, inTTY: true}, &out, &errOut
}

// TestAdminKeyPromptsReadCommandInput drives the name and confirm prompts through
// the command's input rather than the process stdin.
func TestAdminKeyPromptsReadCommandInput(t *testing.T) {
	adminKeyFixture(t)
	cio, out, errOut := promptIO("ci\ny\n")
	if code := runAdminKeyGenerate(cio, adminKeyOpts{}); code != 0 {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
	if !strings.Contains(errOut.String(), "Key name: ") || !strings.Contains(errOut.String(), `Confirm generate admin API key "ci"`) {
		t.Fatalf("want both prompts on stderr, got %q", errOut)
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Fatal("generate must print the new key on stdout")
	}

	cio, _, errOut = promptIO("n\n")
	if code := runAdminKeyDelete(cio, adminKeyOpts{name: "ci"}); code != 1 || !strings.Contains(errOut.String(), "not confirmed") {
		t.Fatalf("a declined delete: exit %d stderr %q", code, errOut)
	}
}

func TestAdminKeyDryRunWithoutNameRefusesToPrompt(t *testing.T) {
	adminKeyFixture(t)
	for _, verb := range []string{"generate", "rotate"} {
		cio, _, errOut := promptIO("should-not-be-read\n")
		run := runAdminKeyGenerate
		if verb == "rotate" {
			run = runAdminKeyRotate
		}
		if code := run(cio, adminKeyOpts{dryRun: true}); code != 2 {
			t.Fatalf("%s --dry-run without --name: exit %d, want 2", verb, code)
		}
		if got := errOut.String(); !strings.Contains(got, "dry-run needs --name") || strings.Contains(got, "Key name") {
			t.Fatalf("%s: want the --name refusal and no prompt, got %q", verb, got)
		}
	}
}

func TestAdminKeyListTextShowsRotation(t *testing.T) {
	recs := []keys.Record{
		{Name: "a", ID: "1", Prefix: "pk_a", CreatedAt: "2026-01-01T00:00:00Z"},
		{Name: "b", ID: "2", Prefix: "pk_b", CreatedAt: "2026-01-01T00:00:00Z", RotatedAt: "2026-02-01T00:00:00Z"},
	}
	cio, out, _ := promptIO("")
	if err := writeAdminKeyList(cio, recs); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "Rotated: never") || !strings.Contains(got, "Rotated: 2026-02-01T00:00:00Z") {
		t.Fatalf("list text must carry RotatedAt, got %q", got)
	}
}
