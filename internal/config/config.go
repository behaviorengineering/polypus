// Package config holds Polypus gateway defaults and runtime options.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	DefaultHost       = "127.0.0.1"
	DefaultPort       = 1320
	DefaultBackendURL = "http://127.0.0.1:1322"
)

// ServeOptions configures the public Polypus HTTP gateway.
type ServeOptions struct {
	Host       string
	Port       int
	BackendURL string
}

// LoadServeOptions reads POLYPUS_* and POLYPUS_BACKEND_URL / POLYPUS_MLX_URL from the environment.
func LoadServeOptions() ServeOptions {
	host := strings.TrimSpace(os.Getenv("POLYPUS_HOST"))
	if host == "" {
		host = DefaultHost
	}
	port := DefaultPort
	if raw := strings.TrimSpace(os.Getenv("POLYPUS_PORT")); raw != "" {
		if p, err := strconv.Atoi(raw); err == nil && p > 0 {
			port = p
		}
	}
	backend := strings.TrimSpace(os.Getenv("POLYPUS_BACKEND_URL"))
	if backend == "" {
		backend = strings.TrimSpace(os.Getenv("POLYPUS_MLX_URL"))
	}
	if backend == "" {
		mlxHost := strings.TrimSpace(os.Getenv("POLYPUS_MLX_HOST"))
		if mlxHost == "" {
			mlxHost = DefaultHost
		}
		mlxPort := 1322
		if raw := strings.TrimSpace(os.Getenv("POLYPUS_MLX_PORT")); raw != "" {
			if p, err := strconv.Atoi(raw); err == nil && p > 0 {
				mlxPort = p
			}
		}
		backend = fmt.Sprintf("http://%s:%d", mlxHost, mlxPort)
	}
	backend = strings.TrimRight(backend, "/")
	return ServeOptions{
		Host:       host,
		Port:       port,
		BackendURL: backend,
	}
}

// ListenAddr returns host:port for the gateway.
func (o ServeOptions) ListenAddr() string {
	return fmt.Sprintf("%s:%d", o.Host, o.Port)
}

// GatewayBaseURL returns the HTTP base URL Switchyard and render use to call back into Polypus.
// Wildcard bind addresses map to loopback so outbound clients can connect.
func (o ServeOptions) GatewayBaseURL() string {
	host := strings.TrimSpace(o.Host)
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = DefaultHost
	}
	port := o.Port
	if port <= 0 {
		port = DefaultPort
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}
