package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
)

func runProcesses(args []string) int {
	fs := flag.NewFlagSet("processes", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "router config path (default POLYPUS_CONFIG resolution)")
	printKey := fs.String("print", "", "print one key as 0/1 for scripts (mlx); unset key prints nothing and exits 3")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *configPath != "" {
		if err := os.Setenv("POLYPUS_CONFIG", *configPath); err != nil {
			fmt.Fprintf(os.Stderr, "polypus processes: set POLYPUS_CONFIG: %v\n", err)
			return 1
		}
	}

	flags, err := config.LoadProcessFlags()
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus processes: %v\n", err)
		return 1
	}

	key := strings.TrimSpace(strings.ToLower(*printKey))
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
