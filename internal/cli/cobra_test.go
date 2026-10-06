package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func captureRun(args []string) (int, string, string) {
	oldOut := os.Stdout
	oldErr := os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr
	code := Run(args)
	_ = wOut.Close()
	_ = wErr.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr
	var outBuf, errBuf bytes.Buffer
	_, _ = io.Copy(&outBuf, rOut)
	_, _ = io.Copy(&errBuf, rErr)
	return code, outBuf.String(), errBuf.String()
}

func TestCommandsUseRunEOnly(t *testing.T) {
	root := newRoot()
	for _, cmd := range polypusCommands(root) {
		if cmd.Name() == "help" {
			continue
		}
		if cmd.Run != nil {
			t.Fatalf("command %q sets Run; use RunE only", cmd.CommandPath())
		}
		if cmd.RunE == nil {
			t.Fatalf("command %q missing RunE", cmd.CommandPath())
		}
	}
}

func TestCommandTreeNames(t *testing.T) {
	root := newRoot()
	wantRoot := []string{
		"version", "processes", "init", "secret", "serve", "switchyard-render", "admin-key",
	}
	gotRoot := commandNames(root.Commands())
	if !sameSet(gotRoot, wantRoot) {
		t.Fatalf("root commands: got %v want %v", gotRoot, wantRoot)
	}
	secret := root.Commands()[indexOf(root.Commands(), "secret")]
	wantSecret := []string{"set"}
	if !sameSet(commandNames(secret.Commands()), wantSecret) {
		t.Fatalf("secret children: got %v want %v", commandNames(secret.Commands()), wantSecret)
	}
	adminKey := root.Commands()[indexOf(root.Commands(), "admin-key")]
	wantAdmin := []string{"list", "generate", "rotate", "delete"}
	if !sameSet(commandNames(adminKey.Commands()), wantAdmin) {
		t.Fatalf("admin-key children: got %v want %v", commandNames(adminKey.Commands()), wantAdmin)
	}
}

func TestAgentGuideListsLeaves(t *testing.T) {
	guide := agentOperatingGuide(newRoot())
	for _, needle := range []string{
		"init",
		"secret set",
		"admin-key list",
		"admin-key generate",
		"serve",
		"version",
	} {
		if !strings.Contains(guide, needle) {
			t.Fatalf("guide missing %q\n%s", needle, guide)
		}
	}
}

func TestRunBareExit0(t *testing.T) {
	code, out, _ := captureRun(nil)
	if code != 0 {
		t.Fatalf("exit %d want 0", code)
	}
	if !strings.Contains(out, "ROLE & BOUNDARIES") {
		t.Fatalf("stdout missing ROLE & BOUNDARIES: %q", out)
	}
	if !strings.Contains(out, "COMMANDS BY RISK") {
		t.Fatalf("stdout missing COMMANDS BY RISK: %q", out)
	}
}

func TestRunHelpListsInitSecret(t *testing.T) {
	code, out, errOut := captureRun([]string{"help"})
	combined := out + errOut
	if code != 0 {
		t.Fatalf("exit %d want 0", code)
	}
	if !strings.Contains(combined, "init") || !strings.Contains(combined, "secret") {
		t.Fatalf("help missing init/secret: stdout=%q stderr=%q", out, errOut)
	}
}

func TestRunUnknownExit2(t *testing.T) {
	code, _, _ := captureRun([]string{"not-a-cmd"})
	if code != 2 {
		t.Fatalf("exit %d want 2", code)
	}
}

func TestProcessesPrintFlagNotUnknown(t *testing.T) {
	code, out, errOut := captureRun([]string{"processes", "--print", "mlx"})
	combined := out + errOut
	if strings.Contains(combined, "unknown flag") {
		t.Fatalf("processes --print mlx should reach legacy parser: %q", combined)
	}
	// Without POLYPUS_CONFIG in test env, exit may be 3 (mlx unset) or 0/1 with config.
	if code == 2 && strings.Contains(errOut, "unknown") {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
}

func TestRunVersion(t *testing.T) {
	code, out, _ := captureRun([]string{"version"})
	if code != 0 {
		t.Fatalf("exit %d want 0", code)
	}
	if !strings.Contains(out, "polypus ") {
		t.Fatalf("stdout %q", out)
	}
}

func commandNames(cmds []*cobra.Command) []string {
	var names []string
	for _, c := range cmds {
		if c.Name() == "help" {
			continue
		}
		names = append(names, c.Name())
	}
	return names
}

func indexOf(cmds []*cobra.Command, name string) int {
	for i, c := range cmds {
		if c.Name() == name {
			return i
		}
	}
	return -1
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	am := make(map[string]int)
	for _, s := range a {
		am[s]++
	}
	for _, s := range b {
		am[s]--
		if am[s] < 0 {
			return false
		}
	}
	for _, n := range am {
		if n != 0 {
			return false
		}
	}
	return true
}
