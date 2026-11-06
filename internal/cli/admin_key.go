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

func openAdminKeyStore() (*keys.Store, error) {
	path, err := config.ResolveAdminKeysPath()
	if err != nil {
		return nil, err
	}
	return keys.Config{Path: path, Clock: func() time.Time { return time.Now().UTC() }}.CreateStore()
}

type adminKeyOpts struct {
	name    string
	yes     bool
	dryRun  bool
	jsonOut bool
}

func newAdminKeyCmd() *cobra.Command {
	adminKey := &cobra.Command{
		Use:     "admin-key",
		Short:   "Gateway access keys (optional lock)",
		GroupID: groupMutate,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := cmd.Help(); err != nil {
				return err
			}
			return exitStatus(2)
		},
	}
	for _, sub := range []adminKeySubcommand{
		{Use: "list", Short: "List gateway access key metadata (no secrets)", Group: groupInspect, Run: runAdminKeyList},
		{Use: "generate", Short: "Create gateway access key (plaintext once)", Group: groupMutate, Mutates: true, Run: runAdminKeyGenerate},
		{Use: "rotate", Short: "Rotate gateway access key secret", Group: groupMutate, Mutates: true, Run: runAdminKeyRotate},
		{Use: "delete", Short: "Remove gateway access key", Group: groupMutate, Mutates: true, Run: runAdminKeyDelete},
	} {
		adminKey.AddCommand(sub.create())
	}
	return adminKey
}

// adminKeySubcommand describes one admin-key verb.
type adminKeySubcommand struct {
	Use, Short, Group string
	// Mutates adds --name, --yes and --dry-run.
	Mutates bool
	Run     func(cmdIO, adminKeyOpts) int
}

// create builds the Cobra command and binds the shared flags.
func (s adminKeySubcommand) create() *cobra.Command {
	var o adminKeyOpts
	c := &cobra.Command{
		Use:     s.Use,
		Short:   s.Short,
		GroupID: s.Group,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return exitStatus(s.Run(ioFor(cmd), o))
		},
	}
	c.Flags().BoolVar(&o.jsonOut, "json", false, "json output")
	if s.Mutates {
		c.Flags().StringVar(&o.name, "name", "", "key name")
		c.Flags().BoolVar(&o.yes, "yes", false, "skip confirmation")
		c.Flags().BoolVar(&o.dryRun, "dry-run", false, "validate without writing (no prompt)")
	}
	return c
}

func runAdminKeyGenerate(cio cmdIO, o adminKeyOpts) int {
	return runAdminKeyMutate(cio, "generate", o, adminKeyMutation{
		apply: func(s *keys.Store, name string) (keys.GenerateResult, error) {
			return s.Generate(cio.ctx, name)
		},
		check: func(s *keys.Store, name string) (string, error) {
			if err := s.CheckGenerate(name); err != nil {
				return "", err
			}
			return "would create " + name, nil
		},
	})
}

func runAdminKeyRotate(cio cmdIO, o adminKeyOpts) int {
	return runAdminKeyMutate(cio, "rotate", o, adminKeyMutation{
		apply: func(s *keys.Store, name string) (keys.GenerateResult, error) {
			return s.Rotate(cio.ctx, name)
		},
		check: func(s *keys.Store, name string) (string, error) {
			rec, err := findAdminKey(s, name)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("would rotate %s (%s)", name, rec.Prefix), nil
		},
	})
}

func runAdminKeyDelete(cio cmdIO, o adminKeyOpts) int {
	if err := requireAdminKeyName(o.name, o.yes || o.dryRun); err != nil {
		cio.errorf("%s\n", err.Error())
		return 2
	}
	s, err := openAdminKeyStore()
	if err != nil {
		cio.errorf("polypus admin-key delete: %v\n", err)
		return 1
	}
	// A dry-run never prompts: it only reads the store.
	if o.dryRun {
		return dryRunDelete(cio, s, o.name)
	}
	if !o.yes && !confirmed(cio, "delete", "delete admin API key", o.name) {
		return 1
	}
	if err := s.Delete(cio.ctx, o.name); err != nil {
		cio.errorf("polypus admin-key delete: %v\n", err)
		return 1
	}
	if o.jsonOut {
		return writeJSONResult(cio, "delete", map[string]string{"name": o.name, "deleted": "true"})
	}
	cio.errorf("polypus admin-key delete: removed %s\n", o.name)
	return 0
}

// dryRunDelete reports what delete would remove, failing when the key is not there.
func dryRunDelete(cio cmdIO, s *keys.Store, name string) int {
	rec, err := findAdminKey(s, name)
	if err != nil {
		cio.errorf("polypus admin-key delete: dry-run: %v\n", err)
		return 1
	}
	cio.errorf("polypus admin-key delete: dry-run: would remove %s (%s)\n", name, rec.Prefix)
	return 0
}

// findAdminKey returns the stored key named name.
func findAdminKey(s *keys.Store, name string) (keys.Record, error) {
	recs, err := s.List()
	if err != nil {
		return keys.Record{}, err
	}
	for _, r := range recs {
		if r.Name == name {
			return r, nil
		}
	}
	return keys.Record{}, fmt.Errorf("no key named %s", name)
}

func runAdminKeyList(cio cmdIO, o adminKeyOpts) int {
	s, err := openAdminKeyStore()
	if err != nil {
		cio.errorf("polypus admin-key list: %v\n", err)
		return 1
	}
	recs, err := s.List()
	if err != nil {
		cio.errorf("polypus admin-key list: %v\n", err)
		return 1
	}
	if o.jsonOut {
		return writeJSONResult(cio, "list", newAdminKeyListJSON(recs))
	}
	if err := writeAdminKeyList(cio, recs); err != nil {
		cio.errorf("polypus admin-key list: write: %v\n", err)
		return 1
	}
	return 0
}

// writeAdminKeyList prints one labelled block per key.
func writeAdminKeyList(cio cmdIO, recs []keys.Record) error {
	if len(recs) == 0 {
		return cio.printf("No admin keys.\n")
	}
	for _, r := range recs {
		if err := cio.printf("Name: %s\nId: %s\nPrefix: %s\nCreated: %s\n\n", r.Name, r.ID, r.Prefix, r.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

// adminKeyListJSON is `admin-key list --json`. It carries metadata only: the
// stored argon2id hash and salt never leave the store.
type adminKeyListJSON struct {
	Keys []adminKeyListItem `json:"keys"`
}

// adminKeyListItem is one key's metadata in `admin-key list --json`.
type adminKeyListItem struct {
	Name      string `json:"name"`
	ID        string `json:"id"`
	Prefix    string `json:"prefix"`
	CreatedAt string `json:"created_at"`
	RotatedAt string `json:"rotated_at,omitempty"`
}

func newAdminKeyListJSON(recs []keys.Record) adminKeyListJSON {
	out := adminKeyListJSON{Keys: make([]adminKeyListItem, 0, len(recs))}
	for _, r := range recs {
		out.Keys = append(out.Keys, adminKeyListItem{
			Name:      r.Name,
			ID:        r.ID,
			Prefix:    r.Prefix,
			CreatedAt: r.CreatedAt,
			RotatedAt: r.RotatedAt,
		})
	}
	return out
}

// adminKeyJSON is `admin-key generate|rotate --json`: the key is shown once.
type adminKeyJSON struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	Key  string `json:"key"`
}

// writeJSONResult prints v to stdout; a failed write is reported and exits 1.
func writeJSONResult(cio cmdIO, verb string, v any) int {
	if err := json.NewEncoder(cio.out).Encode(v); err != nil {
		cio.errorf("polypus admin-key %s: write json: %v\n", verb, err)
		return 1
	}
	return 0
}

// adminKeyMutation is one mutating verb: apply writes, check is its dry-run and
// returns the plan line without writing.
type adminKeyMutation struct {
	apply func(*keys.Store, string) (keys.GenerateResult, error)
	check func(*keys.Store, string) (string, error)
}

func runAdminKeyMutate(cio cmdIO, verb string, o adminKeyOpts, m adminKeyMutation) int {
	name := o.name
	if strings.TrimSpace(name) == "" {
		if !stdinTTY() {
			cio.errorf("%s\n", "polypus admin-key "+verb+": "+noTTYMessage)
			return 2
		}
		prompted, err := promptLine(cio, "Key name: ")
		if err != nil {
			cio.errorf("polypus admin-key %s: %v\n", verb, err)
			return 2
		}
		name = prompted
	}
	if err := requireAdminKeyName(name, o.yes || o.dryRun); err != nil {
		cio.errorf("%s\n", err.Error())
		return 2
	}
	s, err := openAdminKeyStore()
	if err != nil {
		cio.errorf("polypus admin-key %s: %v\n", verb, err)
		return 1
	}
	// A dry-run never prompts: it checks the store the way the write would.
	if o.dryRun {
		plan, err := m.check(s, name)
		if err != nil {
			cio.errorf("polypus admin-key %s: dry-run: %v\n", verb, err)
			return 1
		}
		cio.errorf("polypus admin-key %s: dry-run: %s\n", verb, plan)
		return 0
	}
	if !o.yes && !confirmed(cio, verb, verb+" admin API key", name) {
		return 1
	}
	res, err := m.apply(s, name)
	if err != nil {
		cio.errorf("polypus admin-key %s: %v\n", verb, err)
		return 1
	}
	if o.jsonOut {
		if code := writeJSONResult(cio, verb, adminKeyJSON{Name: res.Record.Name, ID: res.Record.ID, Key: res.Key}); code != 0 {
			// The key is stored but its only copy was lost: say so, so it can be rotated.
			cio.errorf("polypus admin-key %s: %s is stored but its key was not shown; run admin-key rotate --name %s\n", verb, res.Record.Name, res.Record.Name)
			return code
		}
		return 0
	}
	if _, err := fmt.Fprintln(cio.out, res.Key); err != nil {
		cio.errorf("polypus admin-key %s: %s is stored but its key was not shown (%v); run admin-key rotate --name %s\n", verb, res.Record.Name, err, res.Record.Name)
		return 1
	}
	cio.errorf("polypus admin-key %s: stored %s (id=%s); save the key above (shown once)\n", verb, res.Record.Name, res.Record.ID)
	return 0
}

func requireAdminKeyName(name string, yes bool) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("polypus admin-key: name required")
	}
	if !stdinTTY() && !yes {
		return fmt.Errorf("polypus admin-key: %s", noTTYMessage)
	}
	return nil
}

// noTTYMessage says why a prompt cannot run and how to skip it.
const noTTYMessage = "cannot prompt when stdin is not a TTY; use --name and --yes"

func stdinTTY() bool {
	f, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (f.Mode() & os.ModeCharDevice) != 0
}

// confirmed asks the operator to confirm action on name, and says why when the
// answer is not yes: a read failure, a decline, or no terminal to ask on.
func confirmed(cio cmdIO, verb, action, name string) bool {
	ok, err := confirmTTY(cio, action, name)
	switch {
	case err != nil:
		cio.errorf("polypus admin-key %s: read confirmation: %v\n", verb, err)
	case !ok:
		cio.errorf("polypus admin-key %s: not confirmed (pass --yes to skip the prompt)\n", verb)
	}
	return err == nil && ok
}

func confirmTTY(cio cmdIO, action, name string) (bool, error) {
	if !stdinTTY() {
		return false, nil
	}
	line, err := promptLine(cio, fmt.Sprintf("Confirm %s \"%s\"? [y/N]: ", action, name))
	if err != nil {
		return false, err
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes", nil
}

// promptLine writes label to the error stream and reads one line from stdin.
func promptLine(cio cmdIO, label string) (string, error) {
	cio.errorf("%s", label)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
