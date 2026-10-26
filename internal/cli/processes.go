package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/spf13/cobra"
)

type processesOpts struct {
	configPath string
	printKey   string
	jsonOut    bool
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
	cmd.Flags().BoolVar(&o.jsonOut, "json", false, `print {"mlx": true|false|null}`)
	return cmd
}

func runProcesses(o processesOpts) int {
	flags, err := config.LoadProcessFlagsFrom(o.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus processes: %v\n", err)
		return 1
	}

	key := strings.TrimSpace(strings.ToLower(o.printKey))
	if key != "" {
		return printProcessKey(key, flags)
	}

	if o.jsonOut {
		if err := json.NewEncoder(os.Stdout).Encode(struct {
			MLX *bool `json:"mlx"`
		}{flags.MLX}); err != nil {
			fmt.Fprintf(os.Stderr, "polypus processes: write json: %v\n", err)
			return 1
		}
		return 0
	}
	state := "unset (process-compose decides)"
	if flags.MLXSet() {
		state = "disabled"
		if *flags.MLX {
			state = "enabled"
		}
	}
	fmt.Printf("MLX process: %s\n", state)
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
