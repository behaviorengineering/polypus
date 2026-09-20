// polypus-chat-smoke: L1 transport probes against Polypus gateway (standalone).
package main

import (
	"os"

	"github.com/behaviorengineering/polypus/internal/chatsmoke"
)

func main() {
	os.Exit(chatsmoke.Run(os.Args[1:]))
}
