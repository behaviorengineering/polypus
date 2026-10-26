package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/spf13/cobra"
)

type initOpts struct {
	force  bool
	dryRun bool
}

func newInitCmd() *cobra.Command {
	var o initOpts
	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Write ~/.config/polypus/config.yaml from example",
		GroupID: groupSetup,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return exitStatus(runInit(o))
		},
	}
	cmd.Flags().BoolVar(&o.force, "force", false, "overwrite an existing ~/.config/polypus/config.yaml")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "print what init would do and write nothing")
	return cmd
}

func runInit(o initOpts) int {
	if o.dryRun {
		path, err := config.UserConfigFilePath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "polypus init: %v\n", err)
			return 1
		}
		action := "would write"
		if _, statErr := os.Stat(path); statErr == nil {
			action = "would keep existing"
			if o.force {
				action = "would replace"
			}
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "polypus init: %v\n", statErr)
			return 1
		}
		fmt.Fprintf(os.Stderr, "polypus init: dry-run: %s %s (and refresh config.yaml.example beside it)\n", action, path)
		return 0
	}
	written, err := config.InitPolypusUserConfig(config.ConfigYAMLExample, o.force)
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus init: %v\n", err)
		return 1
	}
	if written {
		fmt.Fprintln(os.Stderr, "polypus init: wrote ~/.config/polypus/config.yaml")
	} else {
		fmt.Fprintln(os.Stderr, "polypus init: config already exists (use --force to replace)")
	}
	fmt.Fprintln(os.Stderr, "polypus init: refreshed config.yaml.example")
	return 0
}
