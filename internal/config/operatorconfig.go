package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
	"gopkg.in/yaml.v3"
)

// appName names Polypus to the keyring (its service) and to the XDG config lookup.
const appName = "polypus"

func operatorConfigOptions() operatorconfig.Options {
	return operatorconfig.Options{
		App:        appName,
		ConfigEnv:  "POLYPUS_CONFIG",
		ExtraPaths: polypusConfigExtraPaths(),
	}
}

func polypusConfigExtraPaths() []string {
	var out []string
	if root := strings.TrimSpace(os.Getenv("POLYPUS_ROOT")); root != "" {
		out = append(out, filepath.Join(root, "config.yaml"))
	}
	out = append(out, "config.yaml")
	return out
}

// resolvePolypusSecrets fills env vars from the keyring for secrets declared in config.yaml.
// When secrets is empty, no keyring lookups are performed.
func resolvePolypusSecrets(secrets []operatorconfig.Secret, kr operatorconfig.Keyring) error {
	if len(secrets) == 0 {
		return nil
	}
	opts := operatorConfigOptions()
	opts.Secrets = secrets
	opts.Keyring = kr
	return operatorconfig.ResolveSecrets(opts, kr)
}

// InitPolypusUserConfig writes ~/.config/polypus/config.yaml from the shipped example.
func InitPolypusUserConfig(example []byte, force bool) (bool, error) {
	written, err := operatorconfig.InitUserConfig(operatorConfigOptions(), example, force)
	if err != nil {
		return false, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInternal), "config.InitPolypusUserConfig", "write user config")
	}
	return written, nil
}

// SetPolypusSecret stores a named env secret in the polypus keyring service.
// The name MUST appear under secrets: in the live router config (same discovery as serve).
// When kr is nil, DefaultKeyring() (OS store) is used.
func SetPolypusSecret(envName, value string, kr operatorconfig.Keyring) error {
	const op = "config.SetPolypusSecret"
	envName = strings.TrimSpace(envName)
	if envName == "" {
		return derrors.New(derrors.CodeInvalid, op, "secret env name required")
	}
	if err := RequireDeclaredSecret(envName); err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), op, "set secret").With("env", envName)
	}
	if kr == nil {
		kr = operatorconfig.DefaultKeyring()
	}
	if err := kr.Set(appName, envName, operatorconfig.SanitizeSecret(value)); err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "keyring set").With("env", envName)
	}
	return nil
}

// RequireDeclaredSecret reports whether envName is listed under secrets: in the live config.
func RequireDeclaredSecret(envName string) error {
	const op = "config.RequireDeclaredSecret"
	names, err := declaredSecretEnvs()
	if err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeNotReady), op, "read declared secrets")
	}
	want := strings.TrimSpace(envName)
	if slices.Contains(names, want) {
		return nil
	}
	if len(names) == 0 {
		return derrors.New(derrors.CodeInvalid, op,
			fmt.Sprintf("secret %q is not declared: add it under secrets: in the live config (polypus init, then uncomment secrets:)", want)).
			With("env", want)
	}
	return derrors.New(derrors.CodeInvalid, op,
		fmt.Sprintf("secret %q is not declared under secrets: (declared: %s)", want, strings.Join(names, ", "))).
		With("env", want)
}

func declaredSecretEnvs() ([]string, error) {
	path, err := ResolveConfigPathWithError()
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, fmt.Errorf("no router config found; run polypus init and declare secrets: before secret set")
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a path the operator configures (env or flag), not request input
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var file struct {
		Secrets []operatorconfig.Secret `yaml:"secrets"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&file); err != nil {
		return nil, routerDecodeError(path, err)
	}
	out := make([]string, 0, len(file.Secrets))
	for _, s := range file.Secrets {
		n := strings.TrimSpace(s.Env)
		if n == "" {
			return nil, fmt.Errorf("router config %s: secret env name required", path)
		}
		out = append(out, n)
	}
	return out, nil
}

// UserConfigFilePath is where polypus init writes the user config
// (~/.config/polypus/config.yaml, or under $XDG_CONFIG_HOME).
func UserConfigFilePath() (string, error) {
	return operatorconfig.UserConfigPath(appName, "config.yaml")
}
