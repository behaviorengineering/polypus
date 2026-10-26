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
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return &exitError{code: 2, msg: fmt.Sprintf("polypus: unknown command %q\nRun 'polypus help' for the command list.", args[0])}
			}
			_, err := fmt.Fprint(cmd.OutOrStdout(), agentOperatingGuide(cmd))
			return err
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true

	root.AddGroup(&cobra.Group{ID: groupInspect, Title: "Inspect & Validate"})
	root.AddGroup(&cobra.Group{ID: groupSetup, Title: "Setup & secrets"})
	root.AddGroup(&cobra.Group{ID: groupMutate, Title: "Execute & Mutate"})

	root.AddCommand(newVersionCmd())
	root.AddCommand(newProcessesCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newSecretCmd())
	root.AddCommand(newServeCmd())
	root.AddCommand(newSwitchyardRenderCmd())
	root.AddCommand(newAdminKeyCmd())

	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Aliases: []string{"-version"},
		Short:   "Build identity",
		GroupID: groupInspect,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "polypus %s\n", buildinfo.Version)
			return err
		},
	}
}

func addCLIGroups(cmd *cobra.Command) {
	cmd.AddGroup(
		&cobra.Group{ID: groupInspect, Title: "Inspect & Validate"},
		&cobra.Group{ID: groupSetup, Title: "Setup & secrets"},
		&cobra.Group{ID: groupMutate, Title: "Execute & Mutate"},
	)
}

func newServeCmd() *cobra.Command {
	var host string
	var port int
	var backend string
	cmd := &cobra.Command{
		Use:     "serve",
		Short:   "Start gateway (see --host, --port, --backend)",
		GroupID: groupMutate,
		Args:    cobra.NoArgs,
		Long: `Start the Polypus OpenAI-compatible gateway on loopback.

Environment: POLYPUS_HOST, POLYPUS_PORT, POLYPUS_BACKEND_URL, POLYPUS_MLX_HOST, POLYPUS_MLX_PORT
POLYPUS_OTEL, POLYPUS_OTLP_ENDPOINT, POLYPUS_FAILURE_DUMP_DIR, POLYPUS_SERVICE_NAME
POLYPUS_OTEL_SKIP_PATHS (comma list; default /health; "none" traces all paths)

config processes.mlx (bool) drives whether go tool task serve starts the MLX process.
POLYPUS_ENABLE_MLX still overrides when set.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return exitStatus(runServeWithOpts(cmd.Context(), host, port, backend))
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "gateway listen host (default POLYPUS_HOST or 127.0.0.1)")
	cmd.Flags().IntVar(&port, "port", 0, "gateway listen port (default POLYPUS_PORT or 1320)")
	cmd.Flags().StringVar(&backend, "backend", "", "speech backend base URL (default POLYPUS_BACKEND_URL)")
	return cmd
}
