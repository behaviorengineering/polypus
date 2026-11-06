package main

import (
	"os"

	"github.com/behaviorengineering/polypus/internal/cli"
)

func main() {
	// Thin wiring: the CLI lives in internal/cli. Outside hosts embed the gateway
	// through pkg/polypus, which nothing under internal/ imports.
	os.Exit(cli.Run(os.Args[1:]))
}
