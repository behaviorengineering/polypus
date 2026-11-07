package secrets

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// brokenKeyring is a keyring that cannot be reached, as go-keyring is on a Linux
// runner with no Secret Service: every call fails with an error that is not
// ErrNotFound.
type brokenKeyring struct{}

var errNoSecretService = errors.New("dbus: no Secret Service")

func (brokenKeyring) Get(string, string) (string, error) { return "", errNoSecretService }
func (brokenKeyring) Set(string, string, string) error   { return errNoSecretService }
func (brokenKeyring) Delete(string, string) error        { return errNoSecretService }

// countingKeyring records whether the keyring was read at all.
type countingKeyring struct {
	operatorconfig.Keyring
	gets int
}

func (k *countingKeyring) Get(service, account string) (string, error) {
	k.gets++
	return k.Keyring.Get(service, account)
}

const (
	testApp  = "polypus-work"
	testName = "CF_ACCOUNT_ID"
)

func keyringWith(t *testing.T, value string) *operatorconfig.MemKeyring {
	t.Helper()
	kr := operatorconfig.NewMemKeyring()
	if value != "" {
		if err := kr.Set(testApp, testName, value); err != nil {
			t.Fatal(err)
		}
	}
	return kr
}

func TestResolveWithoutPreference(t *testing.T) {
	for name, tc := range map[string]struct {
		shell, stored string
		want          string
		src           Source
		line          string
	}{
		"neither":       {want: "", src: SourceKeyring},
		"keyring only":  {stored: "kr", want: "kr", src: SourceKeyring},
		"shell only":    {shell: "sh", want: "sh", src: SourceEnv, line: `deploy: CF_ACCOUNT_ID from shell (not in keyring service polypus-work)`},
		"both the same": {shell: "same", stored: "same", want: "same", src: SourceKeyring},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(testName, tc.shell)
			var out bytes.Buffer
			r := &Resolver{Keyring: keyringWith(t, tc.stored), Out: &out}
			got, src, err := r.Resolve(testApp, testName)
			if err != nil || got != tc.want || src != tc.src {
				t.Fatalf("Resolve = %q, %q, %v; want %q, %q", got, src, err, tc.want, tc.src)
			}
			if strings.TrimSpace(out.String()) != tc.line {
				t.Fatalf("printed %q, want %q", out.String(), tc.line)
			}
		})
	}
}

func TestResolveConflictWithoutChooserFails(t *testing.T) {
	t.Setenv(testName, "shell-secret")
	var out bytes.Buffer
	r := &Resolver{Keyring: keyringWith(t, "keyring-secret"), Out: &out}
	_, _, err := r.Resolve(testApp, testName)
	var c *Conflict
	if !errors.As(err, &c) || c.App != testApp || c.Name != testName {
		t.Fatalf("err = %v, want a *Conflict naming the secret", err)
	}
	for _, s := range []string{err.Error(), out.String()} {
		if strings.Contains(s, "shell-secret") || strings.Contains(s, "keyring-secret") {
			t.Fatalf("a value leaked into %q", s)
		}
	}
	if !strings.Contains(err.Error(), "--secrets-from env|keyring") {
		t.Fatalf("err = %v, want the flag named", err)
	}
}

func TestResolveConflictChoiceIsKeptForTheRun(t *testing.T) {
	for _, choice := range []Source{SourceEnv, SourceKeyring} {
		t.Run(string(choice), func(t *testing.T) {
			t.Setenv(testName, "shell-secret")
			var out bytes.Buffer
			asked := 0
			r := &Resolver{
				Keyring: keyringWith(t, "keyring-secret"),
				Out:     &out,
				Choose: func(c *Conflict) (Source, error) {
					asked++
					return choice, nil
				},
			}
			want := map[Source]string{SourceEnv: "shell-secret", SourceKeyring: "keyring-secret"}[choice]
			for range 2 {
				got, src, err := r.Resolve(testApp, testName)
				if err != nil || got != want || src != choice {
					t.Fatalf("Resolve = %q, %q, %v; want %q from %s", got, src, err, want, choice)
				}
			}
			if asked != 1 {
				t.Fatalf("asked %d times, want once per run", asked)
			}
			if choice == SourceEnv && !strings.Contains(out.String(), `CF_ACCOUNT_ID from shell (overrides keyring service polypus-work)`) {
				t.Fatalf("printed %q, want the override line", out.String())
			}
		})
	}
}

func TestResolveChooserFailureKeepsTheConflict(t *testing.T) {
	t.Setenv(testName, "a")
	r := &Resolver{
		Keyring: keyringWith(t, "b"),
		Choose:  func(*Conflict) (Source, error) { return SourceAny, errors.New("no answer") },
	}
	_, _, err := r.Resolve(testApp, testName)
	var c *Conflict
	if !errors.As(err, &c) {
		t.Fatalf("err = %v, want the *Conflict kept", err)
	}
}

func TestResolveUnreachableKeyring(t *testing.T) {
	t.Run("shell set: shell wins, says why", func(t *testing.T) {
		t.Setenv(testName, "sh")
		var out bytes.Buffer
		r := &Resolver{Keyring: brokenKeyring{}, Out: &out}
		got, src, err := r.Resolve(testApp, testName)
		if err != nil || got != "sh" || src != SourceEnv {
			t.Fatalf("Resolve = %q, %q, %v; want the shell value", got, src, err)
		}
		if strings.TrimSpace(out.String()) != "deploy: CF_ACCOUNT_ID from shell (keyring unavailable)" {
			t.Fatalf("printed %q", out.String())
		}
	})
	t.Run("shell empty: the keyring error", func(t *testing.T) {
		t.Setenv(testName, "")
		_, _, err := (&Resolver{Keyring: brokenKeyring{}}).Resolve(testApp, testName)
		if !errors.Is(err, errNoSecretService) || derrors.CodeOf(err) != derrors.CodeFailedPrecondition {
			t.Fatalf("err = %v, want the keyring failure", err)
		}
	})
	t.Run("--secrets-from keyring: the keyring error", func(t *testing.T) {
		t.Setenv(testName, "sh")
		_, _, err := (&Resolver{Keyring: brokenKeyring{}, Prefer: SourceKeyring}).Resolve(testApp, testName)
		if !errors.Is(err, errNoSecretService) {
			t.Fatalf("err = %v, want the keyring failure", err)
		}
	})
}

func TestResolvePreferEnvNeverReadsTheKeyring(t *testing.T) {
	kr := &countingKeyring{Keyring: keyringWith(t, "kr")}
	var out bytes.Buffer
	r := &Resolver{Keyring: kr, Prefer: SourceEnv, Out: &out}
	t.Setenv(testName, "sh")
	if got, src, err := r.Resolve(testApp, testName); err != nil || got != "sh" || src != SourceEnv {
		t.Fatalf("Resolve = %q, %q, %v", got, src, err)
	}
	t.Setenv(testName, "")
	if got, _, err := r.Resolve(testApp, testName); err != nil || got != "" {
		t.Fatalf("unset with env preferred: got %q, %v; want empty", got, err)
	}
	if kr.gets != 0 {
		t.Fatalf("keyring read %d times, want none", kr.gets)
	}
	if !strings.Contains(out.String(), "CF_ACCOUNT_ID from shell (--secrets-from env)") {
		t.Fatalf("printed %q", out.String())
	}
}

func TestResolvePreferKeyringIgnoresTheShell(t *testing.T) {
	t.Setenv(testName, "sh")
	got, src, err := (&Resolver{Keyring: keyringWith(t, "kr"), Prefer: SourceKeyring}).Resolve(testApp, testName)
	if err != nil || got != "kr" || src != SourceKeyring {
		t.Fatalf("Resolve = %q, %q, %v; want the keyring value", got, src, err)
	}
}

func TestParseSource(t *testing.T) {
	for in, want := range map[string]Source{"": SourceAny, "env": SourceEnv, " Keyring ": SourceKeyring} {
		if got, err := ParseSource(in); err != nil || got != want {
			t.Fatalf("ParseSource(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseSource("vault"); derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("err = %v, want an unknown source refused", err)
	}
}

// fileWith returns a secrets file double holding name=value at app's default path.
func fileWith(t *testing.T, value string) *operatorconfig.MemSecretFile {
	t.Helper()
	path, err := operatorconfig.SecretsEncPath(operatorconfig.Options{App: testApp})
	if err != nil {
		t.Fatal(err)
	}
	return &operatorconfig.MemSecretFile{Files: map[string]map[string]string{path: {testName: value}}}
}

// countingFile records how many times the secrets file was decrypted.
type countingFile struct {
	operatorconfig.SecretFile
	loads int
}

func (f *countingFile) Load(path string) (map[string]string, error) {
	f.loads++
	return f.SecretFile.Load(path)
}

func TestResolveSecretsFileIsTheThirdSource(t *testing.T) {
	for name, tc := range map[string]struct {
		shell, stored string
		prefer        Source
		want          string
		src           Source
	}{
		"neither: the file":            {want: "file", src: SourceFile},
		"keyring wins over the file":   {stored: "kr", want: "kr", src: SourceKeyring},
		"shell wins over the file":     {shell: "sh", want: "sh", src: SourceEnv},
		"--secrets-from keyring":       {shell: "sh", prefer: SourceKeyring, want: "file", src: SourceFile},
		"--secrets-from env":           {prefer: SourceEnv, want: "", src: SourceEnv},
		"shell and file differ: shell": {shell: "sh", want: "sh", src: SourceEnv},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(testName, tc.shell)
			r := &Resolver{Keyring: keyringWith(t, tc.stored), SecretFile: fileWith(t, "file"), Prefer: tc.prefer}
			got, src, err := r.Resolve(testApp, testName)
			if err != nil || got != tc.want || src != tc.src {
				t.Fatalf("Resolve = %q, %q, %v; want %q, %q", got, src, err, tc.want, tc.src)
			}
		})
	}
}

func TestResolveSecretsFileDecryptsOncePerRun(t *testing.T) {
	t.Setenv(testName, "")
	t.Setenv("OTHER_NAME", "")
	f := &countingFile{SecretFile: fileWith(t, "file")}
	var out bytes.Buffer
	r := &Resolver{Keyring: keyringWith(t, ""), SecretFile: f, Out: &out}
	for _, n := range []string{testName, "OTHER_NAME", testName} {
		if _, _, err := r.Resolve(testApp, n); err != nil {
			t.Fatal(err)
		}
	}
	if f.loads != 1 {
		t.Fatalf("decrypted %d times, want once", f.loads)
	}
	if strings.Contains(out.String(), "file\n") || !strings.Contains(out.String(), "CF_ACCOUNT_ID from secrets file") {
		t.Fatalf("printed %q, want the source named and no value", out.String())
	}
}

func TestResolveSecretsFileMissingIsAbsent(t *testing.T) {
	t.Setenv(testName, "")
	r := &Resolver{Keyring: keyringWith(t, ""), SecretFile: &operatorconfig.MemSecretFile{}}
	got, src, err := r.Resolve(testApp, testName)
	if err != nil || got != "" || src != SourceKeyring {
		t.Fatalf("Resolve = %q, %q, %v; want absent", got, src, err)
	}
}

type brokenFile struct{}

var errNoAgeKey = errors.New("no age identity")

func (brokenFile) Load(string) (map[string]string, error) { return nil, errNoAgeKey }

func TestResolveSecretsFileDecryptFailureFailsClosed(t *testing.T) {
	t.Setenv(testName, "")
	_, src, err := (&Resolver{Keyring: keyringWith(t, ""), SecretFile: brokenFile{}}).Resolve(testApp, testName)
	if !errors.Is(err, errNoAgeKey) || derrors.CodeOf(err) != derrors.CodeFailedPrecondition || src != SourceFile {
		t.Fatalf("err = %v, src = %q; want the decrypt failure", err, src)
	}
	t.Setenv(testName, "sh")
	if got, _, err := (&Resolver{Keyring: keyringWith(t, ""), SecretFile: brokenFile{}}).Resolve(testApp, testName); err != nil || got != "sh" {
		t.Fatalf("a name the shell holds must not open the file: %q, %v", got, err)
	}
}

func TestExportReadsTheSecretsFileWithoutSettingEnv(t *testing.T) {
	t.Setenv(testName, "")
	id := strings.Repeat("a", 32)
	dir := t.TempDir()
	r := &Resolver{Keyring: keyringWith(t, ""), SecretFile: fileWith(t, id)}
	if err := Export(r, testApp, []string{testName}, filepath.Join(dir, ".env.deploy")); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(testName); got != "" {
		t.Fatalf("Export set %s in the process environment", testName)
	}
	keys, err := NonEmptyDotenvKeys(filepath.Join(dir, ".env.deploy"))
	if err != nil || !keys[testName] {
		t.Fatalf("dotenv keys %v, %v; want %s", keys, err, testName)
	}
}

func TestParseSourceRefusesTheFileSource(t *testing.T) {
	if _, err := ParseSource(string(SourceFile)); derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("err = %v, want sops refused as a --secrets-from choice", err)
	}
}
