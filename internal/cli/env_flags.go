package cli

import (
	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/spf13/cobra"
)

// envBoolFlag is a bool flag whose default, when the operator does not pass it,
// comes from an on/off env variable.
type envBoolFlag struct {
	name string // the flag name.
	env  string // the env variable, read with config.ParseEnvBool.
	dst  *bool  // where the flag stores its value.
}

// envBoolDefaults returns a PreRunE that fills each flag the operator did not pass
// from its env variable (unset means off). A word config.ParseEnvBool does not know
// is a usage error, so the process exits 2 instead of guessing. The value is set on
// dst, not through the flag set, so Flags().Changed still reports only flags the
// operator typed.
func envBoolDefaults(flags ...envBoolFlag) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		for _, fl := range flags {
			if cmd.Flags().Changed(fl.name) {
				continue
			}
			v, err := config.ParseEnvBool(fl.env, false)
			if err != nil {
				return &usageError{err: err}
			}
			*fl.dst = v
		}
		return nil
	}
}
