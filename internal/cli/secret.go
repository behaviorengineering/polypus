package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	"golang.org/x/term"
)

func runSecret(args []string) int {
	if len(args) == 0 {
		printSecretUsage()
		return 2
	}
	switch args[0] {
	case "set":
		return runSecretSet(args[1:])
	case "help", "-h", "--help":
		printSecretUsage()
		return 0
	default:
		printSecretUsage()
		return 2
	}
}

func printSecretUsage() {
	fmt.Fprintf(os.Stderr, `usage:
  polypus secret set <ENV>              # prompt (input hidden on a TTY)
  polypus secret set <ENV> <value>      # value visible in shell history; prefer prompt

examples:
  polypus secret set CF_AI_API_KEY
  polypus secret set CF_ACCOUNT_ID

`)
}

func runSecretSet(args []string) int {
	fs := flag.NewFlagSet("secret set", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) < 1 {
		printSecretUsage()
		return 2
	}
	envName := strings.TrimSpace(rest[0])
	var value string
	if len(rest) >= 2 {
		value = strings.Join(rest[1:], " ")
	} else {
		line, err := readSecretStdin(envName, os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "polypus secret set: %v\n", err)
			return 1
		}
		value = line
	}
	value = strings.TrimSpace(value)
	if value == "" {
		fmt.Fprintln(os.Stderr, "polypus secret set: empty secret value")
		return 1
	}
	if err := config.SetPolypusSecret(envName, value, nil); err != nil {
		fmt.Fprintf(os.Stderr, "polypus secret set: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "polypus secret set: stored %s in keyring (service polypus)\n", envName)
	return 0
}

func readSecretStdin(envName string, r io.Reader) (string, error) {
	f, ok := r.(interface {
		Fd() uintptr
	})
	if ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprintf(os.Stderr, "polypus secret set: enter value for %s (input hidden): ", envName)
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
