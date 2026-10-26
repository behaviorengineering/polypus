package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/spf13/cobra"
)

type processesOpts struct {
	configPath string
	printKey   string
}

func newProcessesCmd() *cobra.Command {
	var o processesOpts
	cmd := &cobra.Command{
		Use:     "processes",
		Short:   "Print process-compose toggles from config",
		GroupID: groupInspect,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return exitStatus(runProcesses(o))
		},
	}
	cmd.Flags().StringVar(&o.configPath, "config", "", "router config path (default POLYPUS_CONFIG resolution)")
	cmd.Flags().StringVar(&o.printKey, "print", "", "print one key as 0/1 for scripts (mlx); an unset key prints nothing and exits 3")
	return cmd
}

func runProcesses(o processesOpts) int {
	if o.configPath != "" {
		if err := os.Setenv("POLYPUS_CONFIG", o.configPath); err != nil {
			fmt.Fprintf(os.Stderr, "polypus processes: set POLYPUS_CONFIG: %v\n", err)
			return 1
		}
	}

	flags, err := config.LoadProcessFlags()
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus processes: %v\n", err)
		return 1
	}

	key := strings.TrimSpace(strings.ToLower(o.printKey))
	if key != "" {
		return printProcessKey(key, flags)
	}

	if flags.MLXSet() {
		fmt.Printf("mlx=%v\n", *flags.MLX)
	} else {
		fmt.Println("mlx=unset")
	}
	return 0
}

func printProcessKey(key string, flags config.ProcessFlags) int {
	switch key {
	case "mlx":
		if !flags.MLXSet() {
			return 3
		}
		if *flags.MLX {
			fmt.Println("1")
		} else {
			fmt.Println("0")
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "polypus processes: unknown --print key %q (want mlx)\n", key)
		return 2
	}
}
