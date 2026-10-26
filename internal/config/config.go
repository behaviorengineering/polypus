// Package config holds Polypus gateway defaults and runtime options.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
)

// Defaults for where the gateway listens and its speech backend.
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
	Keyring    operatorconfig.Keyring // optional; defaults to OS keyring when resolving config secrets.
}

// LoadServeOptions reads POLYPUS_* and POLYPUS_BACKEND_URL / POLYPUS_MLX_URL from the environment.
// A set but invalid port is an error rather than a silent fall back to the default.
func LoadServeOptions() (ServeOptions, error) {
	out, err := loadServeOptions()
	if err != nil {
		return ServeOptions{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), "config.LoadServeOptions", "serve options")
	}
	return out, nil
}

// loadServeOptions is LoadServeOptions before its error is typed at the package boundary.
func loadServeOptions() (ServeOptions, error) {
	host := strings.TrimSpace(os.Getenv("POLYPUS_HOST"))
	if host == "" {
		host = DefaultHost
	}
	port, err := envPort("POLYPUS_PORT", DefaultPort)
	if err != nil {
		return ServeOptions{}, err
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
		mlxPort, err := envPort("POLYPUS_MLX_PORT", 1322)
		if err != nil {
			return ServeOptions{}, err
		}
		backend = fmt.Sprintf("http://%s:%d", mlxHost, mlxPort)
	}
	backend = strings.TrimRight(backend, "/")
	return ServeOptions{
		Host:       host,
		Port:       port,
		BackendURL: backend,
	}, nil
}

// ListenAddr returns host:port for the gateway.
func (o ServeOptions) ListenAddr() string {
	return fmt.Sprintf("%s:%d", o.Host, o.Port)
}

// GatewayBaseURL returns the HTTP base URL Switchyard and render use to call back into Polypus.
// When POLYPUS_BASE_URL is set (e.g. Docker Compose service DNS), it wins over bind address.
// Otherwise wildcard bind addresses map to loopback so outbound clients can connect.
func (o ServeOptions) GatewayBaseURL() string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("POLYPUS_BASE_URL")), "/"); v != "" {
		return v
	}
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

// envPort reads a TCP port from env, or returns def when it is unset.
func envPort(env string, def int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(env))
	if raw == "" {
		return def, nil
	}
	p, err := strconv.Atoi(raw)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("%s=%q is not a port (1-65535)", env, raw)
	}
	return p, nil
}
