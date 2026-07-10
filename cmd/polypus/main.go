package main

import (
	"os"

	"github.com/behaviorengineering/polypus/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
