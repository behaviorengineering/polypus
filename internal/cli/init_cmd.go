package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/behaviorengineering/polypus/internal/config"
)

func runInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "overwrite existing ~/.config/polypus/config.yaml")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	written, err := config.InitPolypusUserConfig(config.ConfigYAMLExample, *force)
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
