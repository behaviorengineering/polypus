package secrets

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// Source says where a deploy secret is read from.
type Source string

// Secret sources.
const (
	// SourceAny reads both the shell and the keyring, and refuses to pick one
	// silently when they hold different values.
	SourceAny Source = ""
	// SourceEnv reads the shell only and never touches the keyring (CI).
	SourceEnv Source = "env"
	// SourceKeyring reads the environment's keyring service, then its SOPS
	// secrets file, and never the shell.
	SourceKeyring Source = "keyring"
	// SourceFile marks a value read from the environment's SOPS secrets file.
	// Resolve returns it; it is not a --secrets-from choice, and as a Prefer it
	// reads like SourceAny.
	SourceFile Source = "sops"
)

// SourceEnvVar sets the secret source when --secrets-from is not passed.
const SourceEnvVar = "POLYPUS_SECRETS_FROM"

// ParseSource reads a --secrets-from value: env, keyring, or empty for both.
func ParseSource(s string) (Source, error) {
	switch v := Source(strings.ToLower(strings.TrimSpace(s))); v {
	case SourceAny, SourceEnv, SourceKeyring:
		return v, nil
	case SourceFile:
	}
	return SourceAny, derrors.New(derrors.CodeInvalid, "secrets.ParseSource",
		fmt.Sprintf("secrets source %q: want env or keyring", s))
}

// Conflict is returned when the shell and the environment's keyring both hold
// Name with different values. Deploy never picks one silently.
type Conflict struct {
	App, Name string
}

func (c *Conflict) Error() string {
	return fmt.Sprintf("%s is set in this shell and differs from keyring service %s; choose one with --secrets-from env|keyring (or %s), or unset it",
		c.Name, c.App, SourceEnvVar)
}

// Resolver reads deploy secrets from the shell, then an environment's keyring
// service, then the environment's optional SOPS secrets file
// (operatorconfig.SecretsEncPath, ~/.config/<app>/secrets.enc.yaml). Only the shell
// and the keyring can conflict: the file fills a name neither holds. It never sets
// anything in the process environment, so the file's age key must come from
// SOPS_AGE_KEY, SOPS_AGE_KEY_FILE or the SOPS default key file. One Resolver serves
// one run: a conflict settled through Choose stays settled for that run, and each
// app's file is decrypted at most once. It is not safe for concurrent use.
type Resolver struct {
	// Keyring holds the environment's secrets; nil uses the OS keyring.
	Keyring operatorconfig.Keyring
	// SecretFile decrypts the environment's secrets file; nil uses SOPS.
	SecretFile operatorconfig.SecretFile
	// Prefer is the operator's --secrets-from choice; SourceAny reads both.
	Prefer Source
	// Choose settles a *Conflict (a terminal prompt) with SourceEnv or
	// SourceKeyring; nil returns the *Conflict.
	Choose func(c *Conflict) (Source, error)
	// Out receives one line per secret taken from the shell; nil discards them.
	Out io.Writer

	chosen map[string]Source
	files  map[string]map[string]string
}

// Resolve returns name for keyring service app and where it came from. A secret in
// no source returns "" with no error, for optional secrets such as the client key.
// With SourceAny, a keyring that cannot be reached counts as absent when the shell
// holds the value; a *Conflict is returned (or settled through Choose) when the
// shell and the keyring answer with different values. The secrets file is read
// only when neither holds the name.
func (r *Resolver) Resolve(app, name string) (string, Source, error) {
	prefer := r.Prefer
	if c, ok := r.chosen[app+"\x00"+name]; ok {
		prefer = c
	}
	shell := operatorconfig.SanitizeSecret(os.Getenv(name))
	switch prefer {
	case SourceEnv:
		if shell == "" {
			return "", SourceEnv, nil
		}
		r.notef("deploy: %s from shell (--secrets-from env)\n", name)
		return shell, SourceEnv, nil
	case SourceKeyring:
		v, err := r.keyring(app, name)
		if err != nil || v != "" {
			return v, SourceKeyring, err
		}
		return r.fromFile(app, name)
	case SourceAny, SourceFile:
	}
	stored, err := r.keyring(app, name)
	switch {
	case err != nil && shell == "":
		return "", SourceKeyring, err
	case err != nil:
		r.notef("deploy: %s from shell (keyring unavailable)\n", name)
		return shell, SourceEnv, nil
	case shell == "" && stored == "":
		return r.fromFile(app, name)
	case shell == "":
		return stored, SourceKeyring, nil
	case stored == "":
		r.notef("deploy: %s from shell (not in keyring service %s)\n", name, app)
		return shell, SourceEnv, nil
	case shell == stored:
		return stored, SourceKeyring, nil
	}
	conflict := &Conflict{App: app, Name: name}
	if r.Choose == nil {
		return "", SourceAny, conflict
	}
	choice, err := r.Choose(conflict)
	if err != nil {
		return "", SourceAny, errors.Join(conflict, err)
	}
	switch choice {
	case SourceEnv:
		r.remember(app, name, choice)
		r.notef("deploy: %s from shell (overrides keyring service %s)\n", name, app)
		return shell, SourceEnv, nil
	case SourceKeyring:
		r.remember(app, name, choice)
		return stored, SourceKeyring, nil
	case SourceAny, SourceFile:
	}
	return "", SourceAny, conflict
}

// fromFile reads name from app's SOPS secrets file. A missing file or a name the
// file lacks is "" from SourceKeyring, as if the lookup had stopped at the keyring.
func (r *Resolver) fromFile(app, name string) (string, Source, error) {
	m, path, err := r.file(app)
	if err != nil {
		return "", SourceFile, err
	}
	v := operatorconfig.SanitizeSecret(m[name])
	if v == "" {
		return "", SourceKeyring, nil
	}
	r.notef("deploy: %s from secrets file %s\n", name, path)
	return v, SourceFile, nil
}

// file decrypts app's secrets file once per run. A missing file is an empty map.
func (r *Resolver) file(app string) (map[string]string, string, error) {
	const op = "secrets.Resolve"
	path, err := operatorconfig.SecretsEncPath(operatorconfig.Options{App: app})
	if err != nil {
		return nil, "", derrors.Wrap(err, derrors.CodeFailedPrecondition, op, "locate secrets file for "+app)
	}
	if m, ok := r.files[app]; ok {
		return m, path, nil
	}
	sf := r.SecretFile
	if sf == nil {
		sf = operatorconfig.SOPSSecretFile{}
	}
	m, err := sf.Load(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		m = map[string]string{}
	case err != nil:
		return nil, path, derrors.Wrap(err, derrors.CodeFailedPrecondition, op, "decrypt secrets file "+path).
			With("hint", "set SOPS_AGE_KEY or SOPS_AGE_KEY_FILE for the file's age key")
	}
	if r.files == nil {
		r.files = make(map[string]map[string]string)
	}
	r.files[app] = m
	return m, path, nil
}

// keyring reads name from keyring service app; a missing entry is "".
func (r *Resolver) keyring(app, name string) (string, error) {
	kr := r.Keyring
	if kr == nil {
		kr = operatorconfig.DefaultKeyring()
	}
	v, err := kr.Get(app, name)
	if errors.Is(err, operatorconfig.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeFailedPrecondition, "secrets.Resolve", "read keyring "+app+":"+name)
	}
	return operatorconfig.SanitizeSecret(v), nil
}

func (r *Resolver) remember(app, name string, s Source) {
	if r.chosen == nil {
		r.chosen = make(map[string]Source)
	}
	r.chosen[app+"\x00"+name] = s
}

// notef writes one line to Out. The lines are advice; a failed write loses only
// the advice, so it is dropped here.
func (r *Resolver) notef(format string, a ...any) {
	if r.Out == nil {
		return
	}
	_, _ = fmt.Fprintf(r.Out, format, a...) //nolint:errcheck // advice only; see above.
}

// missing describes where a required secret was looked for.
func (r *Resolver) missing(app, name string) string {
	switch r.Prefer {
	case SourceEnv:
		return name + " is not set in this shell (--secrets-from env)"
	case SourceKeyring:
		return name + " is not in keyring service " + app + " or its secrets file (store it with: polypus deploy secret set " + name + ")"
	case SourceAny, SourceFile:
	}
	return name + " is not in this shell, keyring service " + app + " or its secrets file (store it with: polypus deploy secret set " + name + ")"
}
