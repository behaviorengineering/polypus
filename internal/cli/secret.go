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
  polypus secret set --stdin <ENV>      # read value from stdin (scripts)
  polypus secret set <ENV> --stdin      # same; trailing --stdin also works

options:
  --stdin              Read secret from stdin
  --password string    Secret on CLI (insecure; prefer prompt or --stdin)
  -p string            Synonym for --password

examples:
  polypus secret set CF_AI_API_KEY
  printf %%s "$TOKEN" | polypus secret set --stdin CF_AI_API_KEY
  printf %%s "$TOKEN" | polypus secret set CF_AI_API_KEY --stdin

`)
}

// normalizeSecretSetArgs moves known flags ahead of the ENV name so Go's
// flag.Parse does not stop at the first non-flag. Without this,
// "secret set CF_ACCOUNT_ID --stdin" stores the literal "--stdin".
func normalizeSecretSetArgs(args []string) []string {
	var flags, positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--stdin" || a == "-stdin":
			flags = append(flags, "--stdin")
		case a == "--password" || a == "-password" || a == "-p":
			flags = append(flags, a)
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i++
			}
		case strings.HasPrefix(a, "--password="), strings.HasPrefix(a, "-p="):
			flags = append(flags, a)
		default:
			positionals = append(positionals, a)
		}
	}
	return append(flags, positionals...)
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
	if err := fs.Parse(normalizeSecretSetArgs(args)); err != nil {
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
		joined := strings.Join(rest[1:], " ")
		if looksLikeCLIFlag(joined) {
			fmt.Fprintf(os.Stderr, "polypus secret set: refusing to store %q as a secret (looks like a flag); use --stdin or a hidden prompt\n", joined)
			return 2
		}
		warnSecretOnCLI()
		value = joined
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

func looksLikeCLIFlag(s string) bool {
	return strings.HasPrefix(s, "-")
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
