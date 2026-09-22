// polypus-smoke: multi-channel L1 probes against a Polypus gateway.
package main

import (
	"os"

	"github.com/behaviorengineering/polypus/pkg/polypus"
)

func main() {
	os.Exit(polypus.SmokeCLI(os.Args[1:]))
}
