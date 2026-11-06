package cli

import (
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/switchyard"
	"github.com/spf13/cobra"
)

type switchyardRenderOpts struct {
	configPath string
	outPath    string
	polypusURL string
	printPath  bool
	dryRun     bool
}

func newSwitchyardRenderCmd() *cobra.Command {
	var o switchyardRenderOpts
	cmd := &cobra.Command{
		Use:     "switchyard-render",
		Short:   "Render routes.toml from config",
		GroupID: groupMutate,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return exitStatus(runSwitchyardRender(ioFor(cmd), o))
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.configPath, "config", "", "router config path (default POLYPUS_CONFIG resolution)")
	f.StringVar(&o.outPath, "out", "", "output TOML path (default switchyard.config_path or cache)")
	f.StringVar(&o.polypusURL, "polypus-url", "", "Polypus gateway base URL for llm_clients.polypus (default from POLYPUS_HOST/PORT)")
	f.BoolVar(&o.printPath, "print-path", false, "write TOML and print output path to stdout (for scripts)")
	f.BoolVar(&o.dryRun, "dry-run", false, "print the rendered TOML to stdout and write no file")
	return cmd
}

func runSwitchyardRender(cio cmdIO, o switchyardRenderOpts) int {
	opts, err := config.LoadServeOptions()
	if err != nil {
		cio.errorf("polypus switchyard-render: %v\n", err)
		return 2
	}
	opts.ConfigPath = o.configPath
	rcfg, err := config.LoadRouterConfig(opts)
	if err != nil {
		cio.errorf("polypus switchyard-render: %v\n", err)
		return 1
	}
	if v := strings.TrimSpace(o.outPath); v != "" {
		rcfg.Switchyard.ConfigPath = v
	}
	renderURL := strings.TrimSpace(o.polypusURL)
	if renderURL == "" {
		renderURL = opts.GatewayBaseURL()
	}
	if o.dryRun {
		raw, err := switchyard.Render(rcfg, renderURL)
		if err != nil {
			cio.errorf("polypus switchyard-render: %v\n", err)
			return 1
		}
		if _, err := cio.out.Write(raw); err != nil {
			cio.errorf("polypus switchyard-render: write stdout: %v\n", err)
			return 1
		}
		return 0
	}
	path, err := switchyard.WriteConfig(rcfg, renderURL)
	if err != nil {
		cio.errorf("polypus switchyard-render: %v\n", err)
		return 1
	}
	if o.printPath {
		if err := cio.printf("%s\n", path); err != nil {
			cio.errorf("polypus switchyard-render: write stdout: %v\n", err)
			return 1
		}
	} else {
		cio.errorf("polypus switchyard-render: wrote %s\n", path)
	}
	return 0
}
