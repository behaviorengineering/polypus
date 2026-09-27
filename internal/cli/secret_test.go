package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestNormalizeSecretSetArgs_trailingStdin(t *testing.T) {
	got := normalizeSecretSetArgs([]string{"CF_ACCOUNT_ID", "--stdin"})
	want := []string{"--stdin", "CF_ACCOUNT_ID"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestNormalizeSecretSetArgs_trailingPassword(t *testing.T) {
	got := normalizeSecretSetArgs([]string{"CF_ACCOUNT_ID", "--password", "secret"})
	want := []string{"--password", "secret", "CF_ACCOUNT_ID"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v want %v", got, want)
	}
}

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

	code := runSecretSet([]string{"NOT_DECLARED", "--stdin"})
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

	code := runSecretSet([]string{"NOT_DECLARED", "--oops"})
	_ = f.Close()
	if code != 2 {
		t.Fatalf("code %d", code)
	}
	out, _ := os.ReadFile(stderrPath)
	if !strings.Contains(string(out), "looks like a flag") {
		t.Fatalf("stderr: %q", out)
	}
}

func TestReadLineStdin_pipe(t *testing.T) {
	got, err := readLineStdin(strings.NewReader("token-value\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "token-value" {
		t.Fatalf("got %q", got)
	}
}

func TestReadLineStdin_pipeNoNewline(t *testing.T) {
	got, err := readLineStdin(bytes.NewReader([]byte("only")))
	if err != nil {
		t.Fatal(err)
	}
	if got != "only" {
		t.Fatalf("got %q", got)
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

	code := runSecretSet([]string{"NOT_DECLARED", "visible"})
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

	code := runSecretSet([]string{"NOT_DECLARED", "--password", "visible"})
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

	code := runSecretSet([]string{})
	_ = w.Close()
	if code != 2 {
		t.Fatalf("code %d", code)
	}
	b, _ := io.ReadAll(r)
	if strings.Contains(string(b), "WARNING!") {
		t.Fatal("usage path should not print insecure warning")
	}
}
