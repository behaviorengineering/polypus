package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// Run dispatches polypus subcommands. Returns a process exit code.
func Run(args []string) int {
	return execute(newRoot(), args)
}

// execute runs a Cobra root and maps its error to a process exit code.
func execute(root *cobra.Command, args []string) int {
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
	if isUnknownCommand(err) || isUsageError(err) {
		fmt.Fprintf(os.Stderr, "%s: %v\nRun '%s help' for the command list.\n", root.Name(), err, root.Name())
		return 2
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}

// isUsageError reports Cobra's argument and flag errors (unknown flag, wrong
// number of arguments), which exit 2 like an unknown command.
func isUsageError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "unknown flag") || strings.Contains(msg, "unknown shorthand flag") ||
		strings.Contains(msg, "accepts ") || strings.Contains(msg, "requires at least") ||
		strings.Contains(msg, "flag needs an argument") || strings.Contains(msg, "invalid argument")
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
