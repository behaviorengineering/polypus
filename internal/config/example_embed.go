package config

import _ "embed"

// ConfigYAMLExample is embedded from internal/config/config.yaml.example.
// That file is generated from repo-root config.yaml.example (make sync-config-example).
//
//go:embed config.yaml.example
var ConfigYAMLExample []byte
