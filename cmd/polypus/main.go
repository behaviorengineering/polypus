package main

import (
	"os"

	"github.com/behaviorengineering/polypus/internal/cli"
)

func main() {
	// Thin wiring: CLI stays in internal/cli; public Serve/Smoke live in pkg/polypus.
	os.Exit(cli.Run(os.Args[1:]))
}
