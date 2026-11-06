package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/deploy"
	"github.com/behaviorengineering/polypus/internal/deploy/clientkey"
	"github.com/behaviorengineering/polypus/internal/deploy/envconfig"
	"github.com/behaviorengineering/polypus/internal/deploy/secrets"
	"github.com/spf13/cobra"
)

// clientKeyBudget bounds one client-key run: a docker exec and two gateway calls.
const clientKeyBudget = 2 * time.Minute

func newDeployClientKeyCmd() *cobra.Command {
	var config, keyName, healthURL string
	var rotate, dryRun, skipVerify bool
	cmd := &cobra.Command{
		Use:   "client-key",
		Short: "Lock the deployed gateway with a client key, stored in the keyring",
		Long: `Generate a client key inside the environment's running gateway, store it in the
keyring service polypus-<identifier> as POLYPUS_API_KEY, then check the gateway now
answers 401 without it and 200 with it. Once a key exists, every request needs it;
polypus deploy smoke sends it. Run polypus deploy run first.`,
		GroupID: groupMutate,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := envconfig.Discover(config)
			if err != nil {
				return err
			}
			url := strings.TrimRight(healthURL, "/")
			if url == "" {
				url = env.GatewayURL()
			}
			cfg := clientkey.Config{Project: env.Name(), KeyName: keyName, Rotate: rotate, HealthURL: url, SkipVerify: skipVerify}
			if dryRun {
				verb := "generate"
				if rotate {
					verb = "rotate"
				}
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "client-key: would %s a key in %s's gateway and store it as %s in keyring service %s\n",
					verb, env.Name(), secrets.ClientKeyName, env.Name())
				return err
			}
			return withTracing(cmd, func(ctx context.Context) error {
				ctx, cancel := context.WithTimeout(ctx, clientKeyBudget)
				defer cancel()
				res, err := clientkey.Lock(ctx, cfg)
				return reportClientKey(cmd.OutOrStdout(), env, res, err)
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&config, "config", "", deployConfigHelp)
	f.StringVar(&keyName, "name", clientkey.DefaultKeyName, "key name in the gateway's key store")
	f.BoolVar(&rotate, "rotate", false, "replace the existing key of that name instead of adding one")
	f.BoolVar(&dryRun, "dry-run", false, "print what would happen; change nothing")
	f.BoolVar(&skipVerify, "skip-verify", false, "skip the 401 and 200 check against the gateway")
	f.StringVar(&healthURL, "health-url", "", healthURLHelp)
	return cmd
}

// reportClientKey prints what clientkey.Lock did: the stored key (even when the
// verify step then failed) and, when it ran, the verified URL.
func reportClientKey(out io.Writer, env envconfig.Environment, res clientkey.Result, err error) error {
	if res.Name != "" {
		if _, werr := fmt.Fprintf(out, "client-key: stored key %s as %s in keyring service %s\n", res.Name, secrets.ClientKeyName, env.Name()); werr != nil {
			return werr
		}
	}
	if err != nil {
		return err
	}
	if res.VerifiedURL == "" {
		return nil
	}
	_, err = fmt.Fprintf(out, "client-key: verified at %s: 401 without the key, 200 with it\n", res.VerifiedURL)
	return err
}

func newDeploySecretCmd() *cobra.Command {
	secret := &cobra.Command{
		Use:     "secret",
		Short:   "Store an environment's secrets in its keyring service",
		GroupID: groupSetup,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := cmd.Help(); err != nil {
				return err
			}
			return exitStatus(2)
		},
	}
	var config string
	var fromStdin, dryRun bool
	set := &cobra.Command{
		Use:   "set <ENV>",
		Short: "Store one secret the environment needs, under keyring service polypus-<identifier>",
		Long: `Store one secret the environment needs, under keyring service polypus-<identifier>.
The name must be one the deploy uses: TS_AUTHKEY, a bearer_env or ${VAR} in the gateway
YAML, or POLYPUS_API_KEY. The value comes from a hidden prompt, or stdin with --stdin.`,
		Example: `  polypus deploy secret set OPENROUTER_API_KEY          # hidden prompt on a TTY
  printf %s "$TS_AUTHKEY" | polypus deploy secret set --stdin TS_AUTHKEY`,
		GroupID: groupSetup,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := envconfig.Discover(config)
			if err != nil {
				return err
			}
			name := strings.TrimSpace(args[0])
			if err := requireEnvironmentSecret(env, name); err != nil {
				return err
			}
			if dryRun {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "secret set: would store %s in keyring service %s\n", name, env.Name())
				return err
			}
			value, err := readDeploySecret(cmd.ErrOrStderr(), name, fromStdin)
			if err != nil {
				return err
			}
			if err := secrets.Store(nil, env.Name(), name, value); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "secret set: stored %s in keyring service %s\n", name, env.Name())
			return err
		},
	}
	f := set.Flags()
	f.StringVar(&config, "config", "", deployConfigHelp)
	f.BoolVar(&fromStdin, "stdin", false, "read the secret from stdin")
	f.BoolVar(&dryRun, "dry-run", false, "check the name and print where it would go; write nothing")
	secret.AddCommand(set)
	return secret
}

// requireEnvironmentSecret refuses names the environment's deploy never reads, so a
// typo does not land unnoticed in the keyring.
func requireEnvironmentSecret(env envconfig.Environment, name string) error {
	names, err := deploy.ExportSecretNames(deploy.Options{GatewayConfig: env.Gateway})
	if err != nil {
		return err
	}
	names = append(names, secrets.ClientKeyName)
	if !slices.Contains(names, name) {
		return &usageError{err: fmt.Errorf("%s is not a secret this environment uses (%s)", name, strings.Join(names, ", "))}
	}
	return nil
}

func readDeploySecret(errOut io.Writer, name string, fromStdin bool) (string, error) {
	if fromStdin {
		return readLineStdin(os.Stdin)
	}
	return readSecretPrompt(errOut, "polypus deploy secret set", name, os.Stdin)
}
