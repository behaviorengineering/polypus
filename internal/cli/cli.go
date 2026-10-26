package cli

import (
	"errors"
	"fmt"
	"os"

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
	markUsageErrors(root)

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
	var usage *usageError
	if errors.As(err, &usage) {
		fmt.Fprintf(os.Stderr, "%s: %v\nRun '%s help' for the command list.\n", root.Name(), err, root.Name())
		return 2
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}

// usageError is a Cobra flag or argument error: the command line is wrong, so the
// process exits 2.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }

func (e *usageError) Unwrap() error { return e.err }

// markUsageErrors types Cobra's flag and argument errors as usageError, so execute
// reads the exit code from the error's type rather than Cobra's wording. A command
// with no Args gets cobra.NoArgs, whose error for a stray word is "unknown command".
func markUsageErrors(root *cobra.Command) {
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &usageError{err: err} })
	for _, cmd := range polypusCommands(root) {
		check := cmd.Args
		if check == nil {
			check = cobra.NoArgs
		}
		cmd.Args = func(c *cobra.Command, args []string) error {
			if err := check(c, args); err != nil {
				return &usageError{err: err}
			}
			return nil
		}
	}
}

// polypusCommands lists root and every Polypus-owned command under it (Cobra's own
// help command excluded).
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
