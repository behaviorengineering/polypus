package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const secretCLIInsecureWarning = "WARNING! Passing the secret on the command line is insecure. Use a hidden prompt or --stdin."

type secretSetOpts struct {
	fromStdin bool
	password  string
	dryRun    bool
}

func newSecretCmd() *cobra.Command {
	secret := &cobra.Command{
		Use:   "secret",
		Short: "Store secrets in the OS keyring",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := cmd.Help(); err != nil {
				return err
			}
			return exitStatus(2)
		},
	}
	addCLIGroups(secret)
	var o secretSetOpts
	setCmd := &cobra.Command{
		Use:     "set <ENV> [value]",
		Short:   "Store CF_AI_API_KEY / CF_ACCOUNT_ID in OS keyring",
		GroupID: groupSetup,
		Args:    cobra.MinimumNArgs(1),
		Example: `  polypus secret set CF_AI_API_KEY                          # hidden prompt on a TTY
  printf %s "$TOKEN" | polypus secret set --stdin CF_AI_API_KEY
  printf %s "$TOKEN" | polypus secret set CF_AI_API_KEY --stdin`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exitStatus(runSecretSet(args, o))
		},
	}
	setCmd.Flags().BoolVar(&o.fromStdin, "stdin", false, "read the secret from stdin")
	setCmd.Flags().StringVarP(&o.password, "password", "p", "", "secret value on the command line (insecure; prefer the prompt or --stdin)")
	setCmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "check the name is declared under secrets: and write nothing")
	secret.AddCommand(setCmd)
	return secret
}

func runSecretSet(args []string, o secretSetOpts) int {
	envName := strings.TrimSpace(args[0])
	if o.dryRun {
		if err := config.RequireDeclaredSecret(envName); err != nil {
			fmt.Fprintf(os.Stderr, "polypus secret set: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "polypus secret set: dry-run: would store %s in keyring (service polypus)\n", envName)
		return 0
	}

	var value string
	switch {
	case len(args) >= 2:
		if o.password != "" || o.fromStdin {
			fmt.Fprintln(os.Stderr, "polypus secret set: provide only one of: command-line value, --password, or --stdin")
			return 2
		}
		joined := strings.Join(args[1:], " ")
		if looksLikeCLIFlag(joined) {
			fmt.Fprintf(os.Stderr, "polypus secret set: refusing to store %q as a secret (looks like a flag); use --stdin or a hidden prompt\n", joined)
			return 2
		}
		warnSecretOnCLI()
		value = joined
	case o.password != "":
		warnSecretOnCLI()
		value = o.password
	case o.fromStdin:
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
