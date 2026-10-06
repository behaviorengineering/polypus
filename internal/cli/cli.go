package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// version is set by GoReleaser via -ldflags -X .../internal/cli.version=...
var version = "dev"

// Run dispatches polypus subcommands. Returns a process exit code.
func Run(args []string) int {
	root := newRoot()
	root.SetArgs(args)
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)

	_, err := root.ExecuteC()
	if err == nil {
		return 0
	}
	var ex *exitError
	if errors.As(err, &ex) {
		if ex.msg != "" {
			fmt.Fprintln(os.Stderr, ex.msg)
		}
		return ex.code
	}
	if isUnknownCommand(err) {
		return 2
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}

func isUnknownCommand(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unknown command")
}

// polypusCommands walks Polypus-owned commands for tests and guide helpers.
func polypusCommands(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd == nil || cmd.Name() == "help" {
			return
		}
		out = append(out, cmd)
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
	return out
}
