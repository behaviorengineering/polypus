package config

import _ "embed"

// ConfigYAMLExample is embedded from internal/config/config.yaml.example.
// That file is a copy of the repo-root config.yaml.example, refreshed by go tool
// task build (and init), which copy it before compiling.
//
//go:embed config.yaml.example
var ConfigYAMLExample []byte
