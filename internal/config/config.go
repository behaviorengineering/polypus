// Package config holds Polypus gateway defaults and runtime options.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
)

// Defaults for where the gateway listens and its speech backend.
const (
	DefaultHost    = "127.0.0.1"
	DefaultPort    = 1320
	DefaultMLXPort = 1322
	// DefaultGatewayURL is where a gateway on the defaults answers.
	DefaultGatewayURL = "http://127.0.0.1:1320"
	// DefaultSwitchyardBaseURL is where the local Switchyard listens.
	DefaultSwitchyardBaseURL = "http://127.0.0.1:4000"
)

// ServeOptions configures the public Polypus HTTP gateway.
type ServeOptions struct {
	Host string
	Port int
	// BackendURL is the MLX speech backend the operator chose. When set it replaces
	// the config's backends.mlx_local.base_url; empty keeps the config's URL (or,
	// with no config file, the default MLX port).
	BackendURL string
	Keyring    operatorconfig.Keyring // optional; defaults to OS keyring when resolving config secrets.
	// ConfigPath is an explicit config file, which must exist; empty resolves
	// POLYPUS_CONFIG, then the user config.
	ConfigPath string
	// BaseURL is the URL Switchyard and render call back into the gateway on
	// (POLYPUS_BASE_URL, for example Docker Compose service DNS). Empty derives it
	// from Host and Port.
	BaseURL string
}

// LoadServeOptions reads POLYPUS_HOST, POLYPUS_PORT and POLYPUS_BASE_URL, and the
// MLX backend from POLYPUS_BACKEND_URL, else POLYPUS_MLX_URL, else POLYPUS_MLX_HOST
// and POLYPUS_MLX_PORT when either is set. With none of the MLX variables set,
// BackendURL stays empty so the config's mlx_local base_url stands. A set but
// invalid port is an error rather than a silent fall back to the default.
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
	backend, err := envBackendURL()
	if err != nil {
		return ServeOptions{}, err
	}
	return ServeOptions{
		Host:       host,
		Port:       port,
		BackendURL: backend,
		BaseURL:    strings.TrimRight(strings.TrimSpace(os.Getenv("POLYPUS_BASE_URL")), "/"),
	}, nil
}

// envBackendURL is the MLX backend URL the environment names, or empty when it
// names none. A set but invalid POLYPUS_MLX_PORT is an error.
func envBackendURL() (string, error) {
	backend := strings.TrimSpace(os.Getenv("POLYPUS_BACKEND_URL"))
	if backend == "" {
		backend = strings.TrimSpace(os.Getenv("POLYPUS_MLX_URL"))
	}
	if backend != "" {
		return strings.TrimRight(backend, "/"), nil
	}
	mlxHost := strings.TrimSpace(os.Getenv("POLYPUS_MLX_HOST"))
	mlxPort, err := envPort("POLYPUS_MLX_PORT", 0)
	if err != nil {
		return "", err
	}
	if mlxHost == "" && mlxPort == 0 {
		return "", nil
	}
	if mlxHost == "" {
		mlxHost = DefaultHost
	}
	if mlxPort == 0 {
		mlxPort = DefaultMLXPort
	}
	return httpBaseURL(mlxHost, mlxPort), nil
}

// ListenAddr returns host:port for the gateway.
func (o ServeOptions) ListenAddr() string {
	return joinHostPort(o.Host, o.Port)
}

// GatewayBaseURL returns the HTTP base URL Switchyard and render use to call back into Polypus.
// BaseURL (POLYPUS_BASE_URL, read once by LoadServeOptions) wins over the bind
// address. Otherwise wildcard bind addresses map to loopback so outbound clients
// can connect.
func (o ServeOptions) GatewayBaseURL() string {
	if v := strings.TrimRight(strings.TrimSpace(o.BaseURL), "/"); v != "" {
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
	return httpBaseURL(host, port)
}

// joinHostPort joins host and port, bracketing an IPv6 host. A host already
// written in brackets ([::1]) is accepted as well.
func joinHostPort(host string, port int) string {
	return net.JoinHostPort(strings.Trim(strings.TrimSpace(host), "[]"), strconv.Itoa(port))
}

// httpBaseURL is the http:// URL for host and port, bracketing an IPv6 host.
func httpBaseURL(host string, port int) string {
	return "http://" + joinHostPort(host, port)
}

// envPort reads a TCP port from env, or returns def when it is unset. def may be 0
// to mean "unset".
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
