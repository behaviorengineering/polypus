// Package cli is the Cobra command tree for the polypus and polypus-smoke
// binaries: bare invoke prints the agent guide, and each command calls one
// service package.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	errOut := root.ErrOrStderr()
	var ex *exitError
	if errors.As(err, &ex) {
		if ex.msg != "" {
			errorf(errOut, "%s\n", ex.msg)
		}
		return ex.code
	}
	var usage *usageError
	if errors.As(err, &usage) {
		errorf(errOut, "%s: %v\nRun '%s help' for the command list.\n", root.Name(), err, root.Name())
		return 2
	}
	errorf(errOut, "%v\n", err)
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

// cmdIO is a command's context, input and output streams, handed to run
// functions so they read and write through Cobra's streams (SetIn, SetOut and
// SetErr reach them) and can be tested without a Cobra tree.
type cmdIO struct {
	ctx    context.Context
	in     io.Reader
	out    io.Writer
	errOut io.Writer
	// inTTY reports whether in is a terminal an operator can answer prompts on.
	inTTY bool
}

// ioFor reads cmd's context and streams.
func ioFor(cmd *cobra.Command) cmdIO {
	in := cmd.InOrStdin()
	return cmdIO{ctx: cmd.Context(), in: in, out: cmd.OutOrStdout(), errOut: cmd.ErrOrStderr(), inTTY: isTerminal(in)}
}

// isTerminal reports whether r is a character device, such as an operator's
// terminal. Pipes, files and in-memory readers are not.
func isTerminal(r io.Reader) bool {
	f, ok := r.(interface{ Stat() (os.FileInfo, error) })
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// errorf writes one diagnostic to the error stream. A failed write to stderr has
// nowhere left to be reported, so it is dropped here, in one place.
func errorf(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...) //nolint:errcheck // stderr is the last resort; see above.
}

// errorf writes one diagnostic to the command's error stream.
func (c cmdIO) errorf(format string, a ...any) { errorf(c.errOut, format, a...) }

// printf writes to the command's output stream. Output is the command's result,
// so a failed write is the caller's to report.
func (c cmdIO) printf(format string, a ...any) error {
	_, err := fmt.Fprintf(c.out, format, a...)
	return err
}
