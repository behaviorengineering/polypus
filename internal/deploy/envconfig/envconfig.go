// Package envconfig loads a polypus deploy environment: the config.yaml that names
// the environment and points at its gateway YAML. Discovery and init go through
// operatorconfig, so an environment is found like every other operator config.
package envconfig

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
	"gopkg.in/yaml.v3"
)

const (
	// configDir is the folder under the user config dir that holds the default
	// environment: ~/.config/polypus/deploy/.
	configDir = "polypus/deploy"
	// ConfigEnv overrides the environment's config.yaml path.
	ConfigEnv = "POLYPUS_DEPLOY_CONFIG"
	// placeholderTailnet is the example's tailnet, which a deploy must not use.
	placeholderTailnet = "example.ts.net"
)

//go:embed config.yaml.example
var configExample []byte

//go:embed gateway.yaml.example
var gatewayExample []byte

var identifierPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// Environment is a loaded deploy environment. Gateway and Images are absolute.
type Environment struct {
	// Path is the config.yaml the environment was loaded from.
	Path          string
	Identifier    string
	Tailnet       string
	Observability bool
	Gateway       string
	// Images is the environment's pinned images file; empty uses the release's own.
	Images string
}

type fileConfig struct {
	Identifier    string `yaml:"identifier"`
	Tailnet       string `yaml:"tailnet"`
	Observability bool   `yaml:"observability"`
	Gateway       string `yaml:"gateway"`
	Images        string `yaml:"images"`
}

// ErrNotFound means discovery found no environment config.
var ErrNotFound = errors.New("no polypus deploy environment")

func options(flagPath string) operatorconfig.Options {
	return operatorconfig.Options{
		App:            configDir,
		ConfigEnv:      ConfigEnv,
		ConfigFlagPath: flagPath,
	}
}

// UserConfigPath is the default environment's config.yaml.
func UserConfigPath() (string, error) {
	p, err := operatorconfig.UserConfigPath(configDir, "config.yaml")
	if err != nil {
		return "", fmt.Errorf("envconfig: user config path: %w", err)
	}
	return p, nil
}

// Resolve finds the environment's config.yaml: flagPath (--config) first, then
// POLYPUS_DEPLOY_CONFIG, then ~/.config/polypus/deploy/config.yaml.
func Resolve(flagPath string) (string, error) {
	p, err := operatorconfig.ResolveConfigPath(options(flagPath))
	if err != nil {
		return "", fmt.Errorf("envconfig: %w", err)
	}
	if p == "" {
		user, uerr := UserConfigPath()
		if uerr != nil {
			return "", uerr
		}
		return "", fmt.Errorf("%w: none at %s (run polypus deploy init, or pass --config)", ErrNotFound, user)
	}
	return p, nil
}

// Discover resolves the environment's config.yaml (see Resolve) and loads it.
func Discover(flagPath string) (Environment, error) {
	p, err := Resolve(flagPath)
	if err != nil {
		return Environment{}, err
	}
	return Load(p)
}

// Load reads and validates the environment config at path. Relative paths in it
// resolve from path's folder.
func Load(path string) (Environment, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Environment{}, fmt.Errorf("envconfig: %q: %w", path, err)
	}
	data, err := os.ReadFile(abs) //nolint:gosec // G304: the operator's own environment config
	if err != nil {
		return Environment{}, fmt.Errorf("envconfig: read %q: %w", abs, err)
	}
	var fc fileConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&fc); err != nil {
		return Environment{}, fmt.Errorf("envconfig: parse %q: %w", abs, err)
	}
	return fc.environment(abs)
}

func (fc fileConfig) environment(path string) (Environment, error) {
	id := strings.TrimSpace(fc.Identifier)
	if id == "" {
		return Environment{}, fmt.Errorf("envconfig: %s: identifier is required", path)
	}
	if !identifierPattern.MatchString(id) {
		return Environment{}, fmt.Errorf("envconfig: %s: identifier %q must be lower-case letters, digits and hyphens", path, id)
	}
	tailnet := strings.TrimSpace(fc.Tailnet)
	if tailnet == "" {
		return Environment{}, fmt.Errorf("envconfig: %s: tailnet is required", path)
	}
	if tailnet == placeholderTailnet {
		return Environment{}, fmt.Errorf("envconfig: %s: tailnet is still the example %q; set your MagicDNS suffix", path, placeholderTailnet)
	}
	gateway := strings.TrimSpace(fc.Gateway)
	if gateway == "" {
		gateway = "gateway.yaml"
	}
	dir := filepath.Dir(path)
	env := Environment{
		Path:          path,
		Identifier:    id,
		Tailnet:       tailnet,
		Observability: fc.Observability,
		Gateway:       relativeTo(dir, gateway),
	}
	if images := strings.TrimSpace(fc.Images); images != "" {
		env.Images = relativeTo(dir, images)
	}
	return env, nil
}

func relativeTo(dir, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(dir, p)
}

// InitResult names the files init writes under Dir and which it wrote (or, from
// PlanInit, would write).
type InitResult struct {
	Dir          string
	ConfigPath   string
	GatewayPath  string
	ExamplePath  string
	WroteConfig  bool
	WroteGateway bool
}

func initPaths() (InitResult, error) {
	cfgPath, err := UserConfigPath()
	if err != nil {
		return InitResult{}, err
	}
	dir := filepath.Dir(cfgPath)
	return InitResult{
		Dir:         dir,
		ConfigPath:  cfgPath,
		GatewayPath: filepath.Join(dir, "gateway.yaml"),
		ExamplePath: cfgPath + ".example",
	}, nil
}

// Init writes the default environment under ~/.config/polypus/deploy/: config.yaml
// and gateway.yaml, each only when missing unless force is set, both mode 0600.
// config.yaml.example beside them is always refreshed.
func Init(force bool) (InitResult, error) {
	res, err := initPaths()
	if err != nil {
		return InitResult{}, err
	}
	if res.WroteConfig, err = operatorconfig.InitUserConfig(options(""), configExample, force); err != nil {
		return InitResult{}, fmt.Errorf("envconfig: init: %w", err)
	}
	if res.WroteGateway, err = writeIfMissing(res.GatewayPath, gatewayExample, force); err != nil {
		return InitResult{}, err
	}
	return res, nil
}

// PlanInit reports what Init would write, without writing.
func PlanInit(force bool) (InitResult, error) {
	res, err := initPaths()
	if err != nil {
		return InitResult{}, err
	}
	res.WroteConfig = force || !exists(res.ConfigPath)
	res.WroteGateway = force || !exists(res.GatewayPath)
	return res, nil
}

func writeIfMissing(path string, data []byte, force bool) (bool, error) {
	if exists(path) && !force {
		return false, nil
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return false, fmt.Errorf("envconfig: write %q: %w", path, err)
	}
	return true, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
