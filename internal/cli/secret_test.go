package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestReadSecretStdin_pipe(t *testing.T) {
	got, err := readSecretStdin("CF_AI_API_KEY", strings.NewReader("token-value\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "token-value" {
		t.Fatalf("got %q", got)
	}
}

func TestReadSecretStdin_pipeNoNewline(t *testing.T) {
	got, err := readSecretStdin("X", bytes.NewReader([]byte("only")))
	if err != nil {
		t.Fatal(err)
	}
	if got != "only" {
		t.Fatalf("got %q", got)
	}
}
