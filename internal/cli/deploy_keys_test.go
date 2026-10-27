package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
)

// memKeyring swaps the process keyring for an in-memory one, so no test writes the
// real OS keyring.
func memKeyring(t *testing.T) *operatorconfig.MemKeyring {
	t.Helper()
	kr := operatorconfig.NewMemKeyring()
	operatorconfig.SetTestKeyring(kr)
	t.Cleanup(func() { operatorconfig.SetTestKeyring(operatorconfig.OSKeyring{}) })
	return kr
}

// withStdin feeds input to os.Stdin for the duration of the test.
func withStdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old })
}

func TestDeploySecretSetStoresUnderEnvironment(t *testing.T) {
	_, config, _ := deployFixture(t)
	kr := memKeyring(t)
	withStdin(t, "sk-or-test-value\n")
	code, out, errOut := captureRun([]string{"deploy", "secret", "set", "--config", config, "--stdin", "OPENROUTER_API_KEY"})
	if code != 0 {
		t.Fatalf("exit %d\nstderr=%s", code, errOut)
	}
	if !strings.Contains(out, "stored OPENROUTER_API_KEY in keyring service polypus-work") {
		t.Fatalf("stdout %q", out)
	}
	if got, _ := kr.Get("polypus-work", "OPENROUTER_API_KEY"); got != "sk-or-test-value" {
		t.Fatalf("stored %q", got)
	}
}

func TestDeploySecretSetRejectsUnusedName(t *testing.T) {
	_, config, _ := deployFixture(t)
	memKeyring(t)
	code, _, errOut := captureRun([]string{"deploy", "secret", "set", "--config", config, "--dry-run", "OPENROUTE_API_KEY"})
	if code != 2 || !strings.Contains(errOut, "not a secret this environment uses") {
		t.Fatalf("exit %d stderr=%s", code, errOut)
	}
}

func TestDeploySecretSetDryRunAcceptsClientKey(t *testing.T) {
	_, config, _ := deployFixture(t)
	kr := memKeyring(t)
	code, out, errOut := captureRun([]string{"deploy", "secret", "set", "--config", config, "--dry-run", "POLYPUS_API_KEY"})
	if code != 0 || !strings.Contains(out, "would store POLYPUS_API_KEY in keyring service polypus-work") {
		t.Fatalf("exit %d stdout=%s stderr=%s", code, out, errOut)
	}
	if _, err := kr.Get("polypus-work", "POLYPUS_API_KEY"); err == nil {
		t.Fatal("dry run stored a secret")
	}
}

func TestDeployClientKeyDryRun(t *testing.T) {
	_, config, _ := deployFixture(t)
	code, out, errOut := captureRun([]string{"deploy", "client-key", "--config", config, "--dry-run", "--rotate"})
	if code != 0 || !strings.Contains(out, "would rotate a key in polypus-work's gateway") {
		t.Fatalf("exit %d stdout=%s stderr=%s", code, out, errOut)
	}
}
