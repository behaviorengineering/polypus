package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
)

func TestLooksLikeCLIFlag(t *testing.T) {
	if !looksLikeCLIFlag("--stdin") {
		t.Fatal("expected --stdin to look like a flag")
	}
	if looksLikeCLIFlag("acct-example-not-a-flag") {
		t.Fatal("account id must not look like a flag")
	}
}

func TestRunSecretSet_trailingStdinReadsPipe(t *testing.T) {
	rIn, wIn, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldIn := os.Stdin
	os.Stdin = rIn
	defer func() { os.Stdin = oldIn }()
	go func() {
		_, _ = io.WriteString(wIn, "pipe-secret-value\n")
		_ = wIn.Close()
	}()

	stderrPath := t.TempDir() + "/stderr"
	f, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	oldErr := os.Stderr
	os.Stderr = f
	defer func() {
		os.Stderr = oldErr
		_ = f.Close()
	}()

	code := Run([]string{"secret", "set", "NOT_DECLARED", "--stdin"})
	_ = f.Close()
	if code == 0 {
		t.Fatal("expected failure for undeclared secret")
	}
	out, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "WARNING!") {
		t.Fatalf("trailing --stdin must not treat flag as CLI value: %q", s)
	}
	if strings.Contains(s, "looks like a flag") {
		t.Fatalf("trailing --stdin must read stdin, not refuse as flag: %q", s)
	}
	if !strings.Contains(s, "polypus secret set:") {
		t.Fatalf("stderr: %q", s)
	}
}

func TestRunSecretSet_rejectsFlagShapedPositional(t *testing.T) {
	stderrPath := t.TempDir() + "/stderr"
	f, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = f
	defer func() {
		os.Stderr = old
		_ = f.Close()
	}()

	code := Run([]string{"secret", "set", "NOT_DECLARED", "--oops"})
	_ = f.Close()
	if code != 2 {
		t.Fatalf("code %d", code)
	}
	out, _ := os.ReadFile(stderrPath)
	if !strings.Contains(string(out), "unknown flag: --oops") {
		t.Fatalf("a flag-shaped value must be refused as an unknown flag, not stored: %q", out)
	}
}

func TestReadSecretStdin_pipe(t *testing.T) {
	got, err := readSecretStdin(strings.NewReader("token-value\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "token-value" {
		t.Fatalf("got %q", got)
	}
}

func TestReadSecretStdin_pipeNoNewline(t *testing.T) {
	got, err := readSecretStdin(bytes.NewReader([]byte("only")))
	if err != nil {
		t.Fatal(err)
	}
	if got != "only" {
		t.Fatalf("got %q", got)
	}
}

func TestValidateSecretValueRefusesMultiLine(t *testing.T) {
	for _, v := range []string{"a\nb", "a\rb", "  first\nsecond\n"} {
		if _, err := validateSecretValue(v); err == nil {
			t.Errorf("%q: a multi-line value must be refused", v)
		}
	}
	if got, err := validateSecretValue("  tok \n"); err != nil || got != "tok" {
		t.Fatalf("got %q, %v", got, err)
	}
}

// secretSetFixture declares CF_AI_API_KEY in a temporary config, swaps the keyring
// for an in-memory one, and feeds stdin to Run. It returns the exit code, stderr
// and the keyring.
func secretSetFixture(t *testing.T, stdin string, args ...string) (int, string, *operatorconfig.MemKeyring) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("secrets:\n  - CF_AI_API_KEY\nbackends:\n  b:\n    base_url: http://127.0.0.1:1\n    capabilities: [chat]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	mem := operatorconfig.NewMemKeyring()
	operatorconfig.SetTestKeyring(mem)
	t.Cleanup(func() { operatorconfig.SetTestKeyring(operatorconfig.OSKeyring{}) })

	rIn, wIn, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldIn := os.Stdin
	os.Stdin = rIn
	t.Cleanup(func() { os.Stdin = oldIn })
	go func() {
		_, _ = io.WriteString(wIn, stdin)
		_ = wIn.Close()
	}()

	stderrPath := filepath.Join(t.TempDir(), "stderr")
	f, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	oldErr := os.Stderr
	os.Stderr = f
	code := Run(append([]string{"secret", "set"}, args...))
	os.Stderr = oldErr
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	return code, string(out), mem
}

func TestSecretSetStdinTrimsTrailingNewline(t *testing.T) {
	code, errOut, mem := secretSetFixture(t, "tok\n", "--stdin", "CF_AI_API_KEY")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	got, err := mem.Get("polypus", "CF_AI_API_KEY")
	if err != nil || got != "tok" {
		t.Fatalf("stored %q, %v; want tok", got, err)
	}
}

func TestSecretSetStdinRefusesMultiLine(t *testing.T) {
	pem := "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC\n-----END PRIVATE KEY-----\n"
	code, errOut, mem := secretSetFixture(t, pem, "--stdin", "CF_AI_API_KEY")
	if code != 1 || !strings.Contains(errOut, "single-line") {
		t.Fatalf("exit %d, want 1 with the single-line rule: %s", code, errOut)
	}
	if got, err := mem.Get("polypus", "CF_AI_API_KEY"); err == nil {
		t.Fatalf("nothing should be stored, got %q", got)
	}
}

func TestSecretSetStdinRefusesOversize(t *testing.T) {
	code, errOut, mem := secretSetFixture(t, strings.Repeat("a", secretStdinMax+1), "--stdin", "CF_AI_API_KEY")
	if code != 1 || !strings.Contains(errOut, "larger than") {
		t.Fatalf("exit %d, want 1 for an oversize secret: %s", code, errOut)
	}
	if got, err := mem.Get("polypus", "CF_AI_API_KEY"); err == nil {
		t.Fatalf("nothing should be stored, got %q", got)
	}
}

func TestSecretSetPasswordRefusesMultiLine(t *testing.T) {
	code, errOut, mem := secretSetFixture(t, "", "CF_AI_API_KEY", "--password", "line1\nline2")
	if code != 1 || !strings.Contains(errOut, "single-line") {
		t.Fatalf("exit %d, want 1 with the single-line rule: %s", code, errOut)
	}
	if _, err := mem.Get("polypus", "CF_AI_API_KEY"); err == nil {
		t.Fatal("nothing should be stored")
	}
}

func TestRunSecretSet_argvWarns(t *testing.T) {
	stderrPath := t.TempDir() + "/stderr"
	f, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = f
	defer func() {
		os.Stderr = old
		_ = f.Close()
	}()

	code := Run([]string{"secret", "set", "NOT_DECLARED", "visible"})
	_ = f.Close()
	if code == 0 {
		t.Fatal("expected failure for undeclared secret")
	}
	b, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	if !strings.Contains(out, "WARNING!") || !strings.Contains(out, "--stdin") {
		t.Fatalf("stderr missing docker-style warning: %q", out)
	}
}

func TestRunSecretSet_passwordFlagWarns(t *testing.T) {
	stderrPath := t.TempDir() + "/stderr"
	f, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = f
	defer func() {
		os.Stderr = old
		_ = f.Close()
	}()

	code := Run([]string{"secret", "set", "NOT_DECLARED", "--password", "visible"})
	_ = f.Close()
	if code == 0 {
		t.Fatal("expected failure for undeclared secret")
	}
	out, _ := os.ReadFile(stderrPath)
	if !strings.Contains(string(out), "WARNING!") {
		t.Fatalf("stderr: %q", out)
	}
}

func TestRunSecretSet_helpNoWarn(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()

	code := Run([]string{"secret", "set"})
	_ = w.Close()
	if code != 2 {
		t.Fatalf("code %d", code)
	}
	b, _ := io.ReadAll(r)
	if strings.Contains(string(b), "WARNING!") {
		t.Fatal("usage path should not print insecure warning")
	}
}

// TestSecretSetReadsCommandInput feeds --stdin through the command's input, not
// the process stdin.
func TestSecretSetReadsCommandInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("secrets:\n  - CF_AI_API_KEY\nbackends:\n  b:\n    base_url: http://127.0.0.1:1\n    capabilities: [chat]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	mem := operatorconfig.NewMemKeyring()
	operatorconfig.SetTestKeyring(mem)
	t.Cleanup(func() { operatorconfig.SetTestKeyring(operatorconfig.OSKeyring{}) })

	var out, errOut bytes.Buffer
	cio := cmdIO{in: strings.NewReader("from-cmd-input\n"), out: &out, errOut: &errOut}
	if code := runSecretSet(cio, []string{"CF_AI_API_KEY"}, secretSetOpts{fromStdin: true}); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if got, err := mem.Get("polypus", "CF_AI_API_KEY"); err != nil || got != "from-cmd-input" {
		t.Fatalf("keyring got %q, %v", got, err)
	}

	out.Reset()
	if code := runSecretSet(cio, []string{"CF_AI_API_KEY"}, secretSetOpts{dryRun: true}); code != 0 || !strings.Contains(out.String(), "dry-run: would store CF_AI_API_KEY") {
		t.Fatalf("dry-run plan must go to stdout: exit %d stdout %q", code, out.String())
	}
}
