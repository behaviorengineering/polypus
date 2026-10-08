package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/admin/keys"
	"github.com/behaviorengineering/polypus/internal/config"
)

// adminKeyNow stamps key metadata; tests may replace.
var adminKeyNow = func() time.Time { return time.Now().UTC() }

func openAdminKeyStore() (*keys.Store, error) {
	return keys.Config{
		Path:  config.ResolveAdminKeysPath(),
		Clock: adminKeyNow,
	}.CreateStore()
}

func runAdminKeyGenerate(args []string) int {
	return runAdminKeyMutate("generate", args, func(s *keys.Store, name string, dry bool) (keys.GenerateResult, error) {
		if dry {
			return keys.GenerateResult{}, nil
		}
		return s.Generate(name)
	})
}

func runAdminKeyRotate(args []string) int {
	return runAdminKeyMutate("rotate", args, func(s *keys.Store, name string, dry bool) (keys.GenerateResult, error) {
		if dry {
			return keys.GenerateResult{}, nil
		}
		return s.Rotate(name)
	})
}

func runAdminKeyDelete(args []string) int {
	fs := flag.NewFlagSet("admin-key delete", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	name := fs.String("name", "", "key name")
	yes := fs.Bool("yes", false, "skip confirmation")
	dry := fs.Bool("dry-run", false, "validate without writing")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := requireAdminKeyName(*name, *yes); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}
	if !*yes && !confirmTTY("delete admin API key", *name) {
		return 1
	}
	if *dry {
		return 0
	}
	s, err := openAdminKeyStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus admin-key delete: %v\n", err)
		return 1
	}
	if err := s.Delete(*name); err != nil {
		fmt.Fprintf(os.Stderr, "polypus admin-key delete: %v\n", err)
		return 1
	}
	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"name": *name, "deleted": "true"})
	} else {
		fmt.Fprintf(os.Stderr, "polypus admin-key delete: removed %s\n", *name)
	}
	return 0
}

func runAdminKeyList(args []string) int {
	fs := flag.NewFlagSet("admin-key list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
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
	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"keys": recs})
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

type adminKeyMutateFn func(*keys.Store, string, bool) (keys.GenerateResult, error)

func runAdminKeyMutate(verb string, args []string, fn adminKeyMutateFn) int {
	fs := flag.NewFlagSet("admin-key "+verb, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	name := fs.String("name", "", "key name")
	yes := fs.Bool("yes", false, "skip confirmation")
	dry := fs.Bool("dry-run", false, "validate without writing")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*name) == "" {
		if !stdinTTY() {
			fmt.Fprintln(os.Stderr, "polypus admin-key "+verb+": cannot prompt when stdin is not a TTY; use --name and --yes")
			return 2
		}
		prompted, err := promptLine("Key name: ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "polypus admin-key %s: %v\n", verb, err)
			return 2
		}
		*name = prompted
	}
	if err := requireAdminKeyName(*name, *yes); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}
	if !*yes && !confirmTTY(verb+" admin API key", *name) {
		return 1
	}
	if *dry {
		fmt.Fprintf(os.Stderr, "polypus admin-key %s: dry-run ok for %q\n", verb, *name)
		return 0
	}
	s, err := openAdminKeyStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus admin-key %s: %v\n", verb, err)
		return 1
	}
	res, err := fn(s, *name, *dry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus admin-key %s: %v\n", verb, err)
		return 1
	}
	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"name": res.Record.Name,
			"id":   res.Record.ID,
			"key":  res.Key,
		})
	} else {
		fmt.Println(res.Key)
		fmt.Fprintf(os.Stderr, "polypus admin-key %s: stored %s (id=%s); save the key above (shown once)\n", verb, res.Record.Name, res.Record.ID)
	}
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

func confirmTTY(action, name string) bool {
	if !stdinTTY() {
		return false
	}
	line, err := promptLine(fmt.Sprintf("Confirm %s \"%s\"? [y/N]: ", action, name))
	if err != nil {
		return false
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
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
