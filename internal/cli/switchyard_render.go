package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/switchyard"
)

func runSwitchyardRender(args []string) int {
	fs := flag.NewFlagSet("switchyard-render", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "router config path (default POLYPUS_CONFIG resolution)")
	outPath := fs.String("out", "", "output TOML path (default switchyard.config_path or cache)")
	polypusURL := fs.String("polypus-url", "", "Polypus gateway base URL for llm_clients.polypus (default from POLYPUS_HOST/PORT)")
	printPath := fs.Bool("print-path", false, "write TOML and print output path to stdout (for scripts)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *configPath != "" {
		_ = os.Setenv("POLYPUS_CONFIG", *configPath)
	}
	opts := config.LoadServeOptions()
	rcfg, err := config.LoadRouterConfig(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus switchyard-render: %v\n", err)
		return 1
	}
	if v := strings.TrimSpace(*outPath); v != "" {
		rcfg.Switchyard.ConfigPath = v
	}
	renderURL := strings.TrimSpace(*polypusURL)
	if renderURL == "" {
		renderURL = opts.GatewayBaseURL()
	}
	path, err := switchyard.WriteConfig(rcfg, renderURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus switchyard-render: %v\n", err)
		return 1
	}
	if *printPath {
		fmt.Println(path)
	} else {
		fmt.Fprintf(os.Stderr, "polypus switchyard-render: wrote %s\n", path)
	}
	return 0
}
