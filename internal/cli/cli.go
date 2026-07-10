package cli

import (
	"fmt"
	"os"
)

// Run dispatches polypus subcommands. Returns a process exit code.
func Run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 2
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return 0
	default:
		printUsage()
		return 2
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `usage:
  polypus serve [flags]   # OpenAI speech API gateway (loopback)

flags:
  --host HOST       gateway listen host
  --port PORT       gateway listen port (default 1320)
  --backend URL     MLX or other OpenAI speech backend

env: POLYPUS_HOST, POLYPUS_PORT, POLYPUS_BACKEND_URL, POLYPUS_MLX_HOST, POLYPUS_MLX_PORT

`)
}
