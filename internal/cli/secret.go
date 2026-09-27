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

const secretCLIInsecureWarning = "WARNING! Passing the secret on the command line is insecure. Use a hidden prompt or --stdin."

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
  polypus secret set <ENV>              # hidden prompt on a TTY
  polypus secret set <ENV> --stdin      # read value from stdin (scripts)

options:
  --stdin              Read secret from stdin
  --password string    Secret on CLI (insecure; prefer prompt or --stdin)
  -p string            Synonym for --password

examples:
  polypus secret set CF_AI_API_KEY
  printf %%s "$TOKEN" | polypus secret set CF_AI_API_KEY --stdin

`)
}

func runSecretSet(args []string) int {
	fs := flag.NewFlagSet("secret set", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var fromStdin bool
	var password string
	var passwordShort string
	fs.BoolVar(&fromStdin, "stdin", false, "read secret from stdin")
	fs.StringVar(&password, "password", "", "secret value (insecure on CLI)")
	fs.StringVar(&passwordShort, "p", "", "synonym for --password")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) < 1 {
		printSecretUsage()
		return 2
	}
	envName := strings.TrimSpace(rest[0])
	if passwordShort != "" {
		password = passwordShort
	}

	var value string
	switch {
	case len(rest) >= 2:
		if password != "" || fromStdin {
			fmt.Fprintln(os.Stderr, "polypus secret set: provide only one of: command-line value, --password, or --stdin")
			return 2
		}
		warnSecretOnCLI()
		value = strings.Join(rest[1:], " ")
	case password != "":
		warnSecretOnCLI()
		value = password
	case fromStdin:
		line, err := readLineStdin(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "polypus secret set: %v\n", err)
			return 1
		}
		value = line
	default:
		line, err := readSecretPrompt(envName, os.Stdin)
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

func warnSecretOnCLI() {
	fmt.Fprintln(os.Stderr, secretCLIInsecureWarning)
}

func readSecretPrompt(envName string, r io.Reader) (string, error) {
	f, ok := r.(interface {
		Fd() uintptr
	})
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return "", fmt.Errorf("cannot prompt when stdin is not a TTY; use --stdin")
	}
	fmt.Fprintf(os.Stderr, "polypus secret set: enter value for %s (input hidden): ", envName)
	b, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func readLineStdin(r io.Reader) (string, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
