// Command polypus-smoke runs multi-channel L1 probes against a Polypus gateway;
// bare invoke prints the agent guide, and run sends the probes.
package main

import (
	"os"

	"github.com/behaviorengineering/polypus/internal/cli"
)

func main() {
	// Thin wiring: the command surface lives in internal/cli.
	os.Exit(cli.RunSmoke(os.Args[1:]))
}
