package cli

import (
	"errors"
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
			return exitStatus(runInit(ioFor(cmd), o))
		},
	}
	cmd.Flags().BoolVar(&o.force, "force", false, "overwrite an existing ~/.config/polypus/config.yaml")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "print what init would do and write nothing")
	return cmd
}

func runInit(cio cmdIO, o initOpts) int {
	path, err := config.UserConfigFilePath()
	if err != nil {
		cio.errorf("polypus init: %v\n", err)
		return 1
	}
	if o.dryRun {
		action := "would write"
		if _, statErr := os.Stat(path); statErr == nil {
			action = "would keep existing"
			if o.force {
				action = "would replace"
			}
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			cio.errorf("polypus init: %v\n", statErr)
			return 1
		}
		cio.errorf("polypus init: dry-run: %s %s (and refresh config.yaml.example beside it)\n", action, path)
		return 0
	}
	written, err := config.InitPolypusUserConfig(config.ConfigYAMLExample, o.force)
	if err != nil {
		cio.errorf("polypus init: %v\n", err)
		return 1
	}
	if written {
		cio.errorf("polypus init: wrote %s\n", path)
	} else {
		cio.errorf("%s\n", "polypus init: config already exists (use --force to replace)")
	}
	cio.errorf("%s\n", "polypus init: refreshed config.yaml.example")
	return 0
}
