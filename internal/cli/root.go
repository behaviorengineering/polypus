package cli

import (
	"fmt"

	"github.com/behaviorengineering/polypus/internal/buildinfo"
	"github.com/spf13/cobra"
)

const (
	groupInspect = "inspect"
	groupSetup   = "setup"
	groupMutate  = "mutate"
)

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "polypus",
		Short:         "OpenAI-compatible inference gateway (loopback)",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       buildinfo.Version,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), agentOperatingGuide(cmd))
			return err
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true

	root.AddCommand(newVersionCmd())
	root.AddCommand(newProcessesCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newSecretCmd())
	root.AddCommand(newServeCmd())
	root.AddCommand(newSwitchyardRenderCmd())
	root.AddCommand(newAdminKeyCmd())
	root.AddCommand(newDeployCmd())
	addUsedGroups(root)
	root.SetVersionTemplate("polypus {{.Version}}\n")
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Build identity",
		GroupID: groupInspect,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "polypus %s\n", buildinfo.Version)
			return err
		},
	}
}

// groupTitles names the help groups, in the order help lists them.
var groupTitles = []cobra.Group{
	{ID: groupInspect, Title: "Inspect & Validate"},
	{ID: groupSetup, Title: "Setup & secrets"},
	{ID: groupMutate, Title: "Execute & Mutate"},
}

// addUsedGroups gives root and every command under it the help groups its
// subcommands use, so help never shows an empty group heading.
func addUsedGroups(root *cobra.Command) {
	for _, cmd := range polypusCommands(root) {
		used := make(map[string]bool)
		for _, c := range cmd.Commands() {
			used[c.GroupID] = true
		}
		for _, g := range groupTitles {
			if used[g.ID] && !cmd.ContainsGroup(g.ID) {
				cmd.AddGroup(&cobra.Group{ID: g.ID, Title: g.Title})
			}
		}
	}
}

func newServeCmd() *cobra.Command {
	var o serveOpts
	cmd := &cobra.Command{
		Use:     "serve",
		Short:   "Start gateway (see --host, --port, --backend)",
		GroupID: groupMutate,
		Args:    cobra.NoArgs,
		Long: `Start the Polypus OpenAI-compatible gateway on loopback.

Config: POLYPUS_CONFIG, else ~/.config/polypus/config.yaml, $POLYPUS_ROOT/config.yaml,
./config.yaml. None found serves the env-only MLX default. A POLYPUS_CONFIG path
that does not exist is an error.
Access keys: POLYPUS_ADMIN_KEYS, else $XDG_STATE_HOME/polypus/admin-api-keys.json
(~/.local/state/polypus/admin-api-keys.json).

Environment: POLYPUS_HOST, POLYPUS_PORT, POLYPUS_BASE_URL (the URL Switchyard calls back on)
POLYPUS_BACKEND_URL (or POLYPUS_MLX_URL, POLYPUS_MLX_HOST, POLYPUS_MLX_PORT) replaces the
config's backends.mlx_local.base_url only when set; --backend does the same.
POLYPUS_OTEL, POLYPUS_OTLP_ENDPOINT, POLYPUS_FAILURE_DUMP_DIR, POLYPUS_SERVICE_NAME
POLYPUS_OTEL_SKIP_PATHS (comma list; default /health; "none" traces all paths)

config processes.mlx (bool) drives whether go tool task serve starts the MLX process.
POLYPUS_ENABLE_MLX still overrides when set.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(ioFor(cmd), o)
		},
	}
	cmd.Flags().StringVar(&o.host, "host", "", "gateway listen host (default POLYPUS_HOST or 127.0.0.1)")
	cmd.Flags().IntVar(&o.port, "port", 0, "gateway listen port (default POLYPUS_PORT or 1320)")
	cmd.Flags().StringVar(&o.backend, "backend", "", "MLX speech backend base URL; replaces the config's mlx_local base_url (default POLYPUS_BACKEND_URL)")
	return cmd
}
