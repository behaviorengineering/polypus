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
				return &exitError{code: 2}
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

func newProcessesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "processes",
		Short:   "Print process-compose toggles from config",
		GroupID: groupInspect,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exitStatus(runProcesses(args))
		},
	}
	cmd.DisableFlagParsing = true
	return cmd
}

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Write ~/.config/polypus/config.yaml from example",
		GroupID: groupSetup,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exitStatus(runInit(args))
		},
	}
	cmd.DisableFlagParsing = true
	return cmd
}

func addCLIGroups(cmd *cobra.Command) {
	cmd.AddGroup(
		&cobra.Group{ID: groupInspect, Title: "Inspect & Validate"},
		&cobra.Group{ID: groupSetup, Title: "Setup & secrets"},
		&cobra.Group{ID: groupMutate, Title: "Execute & Mutate"},
	)
}

func newSecretCmd() *cobra.Command {
	secret := &cobra.Command{
		Use:   "secret",
		Short: "Store secrets in the OS keyring",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = cmd.Help()
			return exitStatus(2)
		},
	}
	addCLIGroups(secret)
	setCmd := &cobra.Command{
		Use:     "set",
		Short:   "Store CF_AI_API_KEY / CF_ACCOUNT_ID in OS keyring",
		GroupID: groupSetup,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exitStatus(runSecretSet(args))
		},
	}
	setCmd.DisableFlagParsing = true
	secret.AddCommand(setCmd)
	return secret
}

func newServeCmd() *cobra.Command {
	var host string
	var port int
	var backend string
	cmd := &cobra.Command{
		Use:     "serve",
		Short:   "Start gateway (see --host, --port, --backend)",
		GroupID: groupMutate,
		Long: `Start the Polypus OpenAI-compatible gateway on loopback.

Environment: POLYPUS_HOST, POLYPUS_PORT, POLYPUS_BACKEND_URL, POLYPUS_MLX_HOST, POLYPUS_MLX_PORT
POLYPUS_OTEL, POLYPUS_OTLP_ENDPOINT, POLYPUS_FAILURE_DUMP_DIR, POLYPUS_SERVICE_NAME
POLYPUS_OTEL_SKIP_PATHS (comma list; default /health; "none" traces all paths)

config processes.mlx (bool) drives whether make serve starts the MLX process.
POLYPUS_ENABLE_MLX still overrides when set.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return exitStatus(2)
			}
			return exitStatus(runServeWithOpts(host, port, backend))
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "gateway listen host (default POLYPUS_HOST or 127.0.0.1)")
	cmd.Flags().IntVar(&port, "port", 0, "gateway listen port (default POLYPUS_PORT or 1320)")
	cmd.Flags().StringVar(&backend, "backend", "", "speech backend base URL (default POLYPUS_BACKEND_URL)")
	return cmd
}

func newSwitchyardRenderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "switchyard-render",
		Short:   "Render routes.toml from config",
		GroupID: groupMutate,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exitStatus(runSwitchyardRender(args))
		},
	}
	cmd.DisableFlagParsing = true
	return cmd
}

func newAdminKeyCmd() *cobra.Command {
	adminKey := &cobra.Command{
		Use:   "admin-key",
		Short: "Gateway access keys (optional lock)",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = cmd.Help()
			return exitStatus(2)
		},
	}
	addCLIGroups(adminKey)
	for _, spec := range []struct {
		use     string
		short   string
		groupID string
		run     func([]string) int
	}{
		{"list", "List gateway access key metadata (no secrets)", groupInspect, runAdminKeyList},
		{"generate", "Create gateway access key (plaintext once)", groupMutate, runAdminKeyGenerate},
		{"rotate", "Rotate gateway access key secret", groupMutate, runAdminKeyRotate},
		{"delete", "Remove gateway access key", groupMutate, runAdminKeyDelete},
	} {
		run := spec.run
		c := &cobra.Command{
			Use:     spec.use,
			Short:   spec.short,
			GroupID: spec.groupID,
			RunE: func(cmd *cobra.Command, args []string) error {
				return exitStatus(run(args))
			},
		}
		c.DisableFlagParsing = true
		adminKey.AddCommand(c)
	}
	return adminKey
}
