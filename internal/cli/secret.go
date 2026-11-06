package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const secretCLIInsecureWarning = "WARNING! Passing the secret on the command line is insecure. Use a hidden prompt or --stdin."

// secretStdinMax bounds what secret set reads from stdin. Keys and tokens are far
// smaller; anything larger is a wrong pipe, not a secret.
const secretStdinMax = 64 << 10

type secretSetOpts struct {
	fromStdin bool
	password  string
	dryRun    bool
}

func newSecretCmd() *cobra.Command {
	secret := &cobra.Command{
		Use:     "secret",
		Short:   "Store secrets in the OS keyring",
		GroupID: groupSetup,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := cmd.Help(); err != nil {
				return err
			}
			return exitStatus(2)
		},
	}
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
			return exitStatus(runSecretSet(ioFor(cmd), args, o))
		},
	}
	setCmd.Flags().BoolVar(&o.fromStdin, "stdin", false, "read the secret from stdin")
	setCmd.Flags().StringVarP(&o.password, "password", "p", "", "secret value on the command line (insecure; prefer the prompt or --stdin)")
	setCmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "check the name is declared under secrets: and write nothing")
	secret.AddCommand(setCmd)
	return secret
}

func runSecretSet(cio cmdIO, args []string, o secretSetOpts) int {
	envName := strings.TrimSpace(args[0])
	if o.dryRun {
		if err := config.RequireDeclaredSecret(envName); err != nil {
			cio.errorf("polypus secret set: %v\n", err)
			return 1
		}
		cio.errorf("polypus secret set: dry-run: would store %s in keyring (service polypus)\n", envName)
		return 0
	}

	var value string
	switch {
	case len(args) >= 2:
		if o.password != "" || o.fromStdin {
			cio.errorf("%s\n", "polypus secret set: provide only one of: command-line value, --password, or --stdin")
			return 2
		}
		joined := strings.Join(args[1:], " ")
		if looksLikeCLIFlag(joined) {
			cio.errorf("polypus secret set: refusing to store %q as a secret (looks like a flag); use --stdin or a hidden prompt\n", joined)
			return 2
		}
		warnSecretOnCLI(cio.errOut)
		value = joined
	case o.password != "":
		warnSecretOnCLI(cio.errOut)
		value = o.password
	case o.fromStdin:
		line, err := readSecretStdin(os.Stdin)
		if err != nil {
			cio.errorf("polypus secret set: %v\n", err)
			return 1
		}
		value = line
	default:
		line, err := readSecretPrompt(cio.errOut, "polypus secret set", envName, os.Stdin)
		if err != nil {
			cio.errorf("polypus secret set: %v\n", err)
			return 1
		}
		value = line
	}

	value, err := validateSecretValue(value)
	if err != nil {
		cio.errorf("polypus secret set: %v\n", err)
		return 1
	}
	if value == "" {
		cio.errorf("%s\n", "polypus secret set: empty secret value")
		return 1
	}
	if err := config.SetPolypusSecret(envName, value, nil); err != nil {
		cio.errorf("polypus secret set: %v\n", err)
		return 1
	}
	cio.errorf("polypus secret set: stored %s in keyring (service polypus)\n", envName)
	return 0
}

func looksLikeCLIFlag(s string) bool {
	return strings.HasPrefix(s, "-")
}

func warnSecretOnCLI(w io.Writer) {
	errorf(w, "%s\n", secretCLIInsecureWarning)
}

// readSecretPrompt reads a secret from a hidden prompt written to errOut; cmd names
// the command in it.
func readSecretPrompt(errOut io.Writer, cmd, envName string, r io.Reader) (string, error) {
	f, ok := r.(interface {
		Fd() uintptr
	})
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return "", fmt.Errorf("cannot prompt when stdin is not a TTY; use --stdin")
	}
	errorf(errOut, "%s: enter value for %s (input hidden): ", cmd, envName)
	b, err := term.ReadPassword(int(f.Fd()))
	errorf(errOut, "\n")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// readSecretStdin reads all of stdin up to secretStdinMax and returns it trimmed by
// validateSecretValue. A multi-line value is refused rather than cut at the first
// line, so a pasted PEM or JSON document is never stored in part.
func readSecretStdin(r io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, secretStdinMax+1))
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	if len(b) > secretStdinMax {
		return "", fmt.Errorf("secret on stdin is larger than %d bytes", secretStdinMax)
	}
	return validateSecretValue(string(b))
}

// errMultiLineSecret refuses a value operatorconfig cannot store whole.
var errMultiLineSecret = errors.New("secrets are single-line; multi-line values are not supported")

// validateSecretValue trims v with operatorconfig's rule (leading and trailing
// whitespace and line endings) and refuses a value that still spans lines.
func validateSecretValue(v string) (string, error) {
	v = operatorconfig.SanitizeSecret(v)
	if strings.ContainsAny(v, "\r\n") {
		return "", errMultiLineSecret
	}
	return v, nil
}
