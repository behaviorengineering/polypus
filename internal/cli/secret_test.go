package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

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
