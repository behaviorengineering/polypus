package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
	"gopkg.in/yaml.v3"
)

func operatorConfigOptions() operatorconfig.Options {
	return operatorconfig.Options{
		App:        "polypus",
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

// ResolvePolypusSecrets fills env vars from the keyring for secrets declared in config.yaml.
// When secrets is empty, no keyring lookups are performed.
func ResolvePolypusSecrets(secrets []operatorconfig.Secret, kr operatorconfig.Keyring) error {
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
	return operatorconfig.InitUserConfig(operatorConfigOptions(), example, force)
}

// SetPolypusSecret stores a named env secret in the polypus keyring service.
// The name MUST appear under secrets: in the live router config (same discovery as serve).
// When kr is nil, DefaultKeyring() (OS store) is used.
func SetPolypusSecret(envName, value string, kr operatorconfig.Keyring) error {
	envName = strings.TrimSpace(envName)
	if envName == "" {
		return fmt.Errorf("secret env name required")
	}
	if err := RequireDeclaredSecret(envName); err != nil {
		return err
	}
	if kr == nil {
		kr = operatorconfig.DefaultKeyring()
	}
	return kr.Set("polypus", envName, operatorconfig.SanitizeSecret(value))
}

// RequireDeclaredSecret reports whether envName is listed under secrets: in the live config.
func RequireDeclaredSecret(envName string) error {
	names, err := declaredSecretEnvs()
	if err != nil {
		return err
	}
	want := strings.TrimSpace(envName)
	for _, n := range names {
		if n == want {
			return nil
		}
	}
	if len(names) == 0 {
		return fmt.Errorf("secret %q is not declared: add it under secrets: in the live config (polypus init, then uncomment secrets:)", want)
	}
	return fmt.Errorf("secret %q is not declared under secrets: (declared: %s)", want, strings.Join(names, ", "))
}

func declaredSecretEnvs() ([]string, error) {
	path, err := ResolveConfigPathWithError()
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, fmt.Errorf("no router config found; run polypus init and declare secrets: before secret set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var file struct {
		Secrets []operatorconfig.Secret `yaml:"secrets"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("router config %s: %w", path, err)
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
