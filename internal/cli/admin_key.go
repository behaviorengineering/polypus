package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/admin/keys"
	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/spf13/cobra"
)

// adminKeyNow stamps key metadata; tests may replace.
var adminKeyNow = func() time.Time { return time.Now().UTC() }

func openAdminKeyStore() (*keys.Store, error) {
	path, err := config.ResolveAdminKeysPath()
	if err != nil {
		return nil, err
	}
	return keys.Config{Path: path, Clock: adminKeyNow}.CreateStore()
}

type adminKeyOpts struct {
	name    string
	yes     bool
	dryRun  bool
	jsonOut bool
}

func newAdminKeyCmd() *cobra.Command {
	adminKey := &cobra.Command{
		Use:   "admin-key",
		Short: "Gateway access keys (optional lock)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := cmd.Help(); err != nil {
				return err
			}
			return exitStatus(2)
		},
	}
	adminKey.AddCommand(newAdminKeySubcommand("list", "List gateway access key metadata (no secrets)", groupInspect, false, runAdminKeyList))
	adminKey.AddCommand(newAdminKeySubcommand("generate", "Create gateway access key (plaintext once)", groupMutate, true, runAdminKeyGenerate))
	adminKey.AddCommand(newAdminKeySubcommand("rotate", "Rotate gateway access key secret", groupMutate, true, runAdminKeyRotate))
	adminKey.AddCommand(newAdminKeySubcommand("delete", "Remove gateway access key", groupMutate, true, runAdminKeyDelete))
	return adminKey
}

// newAdminKeySubcommand binds the shared flags; mutating verbs also get --name,
// --yes and --dry-run.
func newAdminKeySubcommand(use, short, group string, mutates bool, run func(adminKeyOpts) int) *cobra.Command {
	var o adminKeyOpts
	c := &cobra.Command{
		Use:     use,
		Short:   short,
		GroupID: group,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return exitStatus(run(o))
		},
	}
	c.Flags().BoolVar(&o.jsonOut, "json", false, "json output")
	if mutates {
		c.Flags().StringVar(&o.name, "name", "", "key name")
		c.Flags().BoolVar(&o.yes, "yes", false, "skip confirmation")
		c.Flags().BoolVar(&o.dryRun, "dry-run", false, "validate without writing")
	}
	return c
}

func runAdminKeyGenerate(o adminKeyOpts) int {
	return runAdminKeyMutate("generate", o, func(s *keys.Store, name string) (keys.GenerateResult, error) {
		return s.Generate(name)
	})
}

func runAdminKeyRotate(o adminKeyOpts) int {
	return runAdminKeyMutate("rotate", o, func(s *keys.Store, name string) (keys.GenerateResult, error) {
		return s.Rotate(name)
	})
}

func runAdminKeyDelete(o adminKeyOpts) int {
	if err := requireAdminKeyName(o.name, o.yes); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}
	if !o.yes && !confirmed("delete", "delete admin API key", o.name) {
		return 1
	}
	s, err := openAdminKeyStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus admin-key delete: %v\n", err)
		return 1
	}
	if o.dryRun {
		return dryRunDelete(s, o.name)
	}
	if err := s.Delete(o.name); err != nil {
		fmt.Fprintf(os.Stderr, "polypus admin-key delete: %v\n", err)
		return 1
	}
	if o.jsonOut {
		return writeJSONResult("delete", map[string]string{"name": o.name, "deleted": "true"})
	}
	fmt.Fprintf(os.Stderr, "polypus admin-key delete: removed %s\n", o.name)
	return 0
}

// dryRunDelete reports what delete would remove, failing when the key is not there.
func dryRunDelete(s *keys.Store, name string) int {
	recs, err := s.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus admin-key delete: %v\n", err)
		return 1
	}
	for _, r := range recs {
		if r.Name == name {
			fmt.Fprintf(os.Stderr, "polypus admin-key delete: dry-run: would remove %s (%s)\n", name, r.Prefix)
			return 0
		}
	}
	fmt.Fprintf(os.Stderr, "polypus admin-key delete: dry-run: no key named %s\n", name)
	return 1
}

func runAdminKeyList(o adminKeyOpts) int {
	s, err := openAdminKeyStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus admin-key list: %v\n", err)
		return 1
	}
	recs, err := s.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus admin-key list: %v\n", err)
		return 1
	}
	if o.jsonOut {
		return writeJSONResult("list", adminKeyListJSON{Keys: recs})
	}
	if len(recs) == 0 {
		fmt.Println("No admin keys.")
		return 0
	}
	for _, r := range recs {
		fmt.Printf("Name: %s\n", r.Name)
		fmt.Printf("Id: %s\n", r.ID)
		fmt.Printf("Prefix: %s\n", r.Prefix)
		fmt.Printf("Created: %s\n", r.CreatedAt)
		fmt.Println()
	}
	return 0
}

// writeJSONResult prints v to stdout; a failed write is reported and exits 1.
// adminKeyListJSON is `admin-key list --json`.
type adminKeyListJSON struct {
	Keys []keys.Record `json:"keys"`
}

// adminKeyJSON is `admin-key generate|rotate --json`: the key is shown once.
type adminKeyJSON struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	Key  string `json:"key"`
}

func writeJSONResult(verb string, v any) int {
	if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "polypus admin-key %s: write json: %v\n", verb, err)
		return 1
	}
	return 0
}

type adminKeyMutateFn func(*keys.Store, string) (keys.GenerateResult, error)

func runAdminKeyMutate(verb string, o adminKeyOpts, fn adminKeyMutateFn) int {
	name := o.name
	if strings.TrimSpace(name) == "" {
		if !stdinTTY() {
			fmt.Fprintln(os.Stderr, "polypus admin-key "+verb+": cannot prompt when stdin is not a TTY; use --name and --yes")
			return 2
		}
		prompted, err := promptLine("Key name: ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "polypus admin-key %s: %v\n", verb, err)
			return 2
		}
		name = prompted
	}
	if err := requireAdminKeyName(name, o.yes); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}
	if !o.yes && !confirmed(verb, verb+" admin API key", name) {
		return 1
	}
	if o.dryRun {
		fmt.Fprintf(os.Stderr, "polypus admin-key %s: dry-run ok for %q\n", verb, name)
		return 0
	}
	s, err := openAdminKeyStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus admin-key %s: %v\n", verb, err)
		return 1
	}
	res, err := fn(s, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus admin-key %s: %v\n", verb, err)
		return 1
	}
	if o.jsonOut {
		if code := writeJSONResult(verb, adminKeyJSON{Name: res.Record.Name, ID: res.Record.ID, Key: res.Key}); code != 0 {
			// The key is stored but its only copy was lost: say so, so it can be rotated.
			fmt.Fprintf(os.Stderr, "polypus admin-key %s: %s is stored but its key was not shown; run admin-key rotate --name %s\n", verb, res.Record.Name, res.Record.Name)
			return code
		}
		return 0
	}
	if _, err := fmt.Println(res.Key); err != nil {
		fmt.Fprintf(os.Stderr, "polypus admin-key %s: %s is stored but its key was not shown (%v); run admin-key rotate --name %s\n", verb, res.Record.Name, err, res.Record.Name)
		return 1
	}
	fmt.Fprintf(os.Stderr, "polypus admin-key %s: stored %s (id=%s); save the key above (shown once)\n", verb, res.Record.Name, res.Record.ID)
	return 0
}

func requireAdminKeyName(name string, yes bool) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("polypus admin-key: name required")
	}
	if !stdinTTY() && !yes {
		return fmt.Errorf("polypus admin-key: cannot prompt when stdin is not a TTY; use --name and --yes")
	}
	return nil
}

func stdinTTY() bool {
	f, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (f.Mode() & os.ModeCharDevice) != 0
}

// confirmed asks the operator to confirm action on name, and says why when the
// answer is not yes: a read failure, a decline, or no terminal to ask on.
func confirmed(verb, action, name string) bool {
	ok, err := confirmTTY(action, name)
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "polypus admin-key %s: read confirmation: %v\n", verb, err)
	case !ok:
		fmt.Fprintf(os.Stderr, "polypus admin-key %s: not confirmed (pass --yes to skip the prompt)\n", verb)
	}
	return err == nil && ok
}

func confirmTTY(action, name string) (bool, error) {
	if !stdinTTY() {
		return false, nil
	}
	line, err := promptLine(fmt.Sprintf("Confirm %s \"%s\"? [y/N]: ", action, name))
	if err != nil {
		return false, err
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes", nil
}

func promptLine(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
