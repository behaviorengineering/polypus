// Package tsserve renders the Tailscale serve config for a deploy: HTTPS on 443 to
// the gateway, and the observability ports forwarded only when those services run.
package tsserve

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// certDomain is expanded by the Tailscale container to the node's own name.
const certDomain = "${TS_CERT_DOMAIN}:443"

// observabilityForwards are the tailnet ports forwarded to the collector, Phoenix
// and HyperDX inside the compose network.
var observabilityForwards = map[string]string{
	"4317": "otelcol:4317",
	"4318": "otelcol:4318",
	"6006": "phoenix:6006",
	"8080": "hyperdx:8080",
}

type tcpHandler struct {
	HTTPS      bool   `json:"HTTPS,omitempty"`
	TCPForward string `json:"TCPForward,omitempty"`
}

type webHandler struct {
	Proxy string `json:"Proxy"`
}

type webServer struct {
	Handlers map[string]webHandler `json:"Handlers"`
}

type serveConfig struct {
	TCP         map[string]tcpHandler `json:"TCP"`
	Web         map[string]webServer  `json:"Web"`
	AllowFunnel map[string]bool       `json:"AllowFunnel"`
}

// Render returns serve.json: 443 proxies to the gateway, and with observability the
// OTLP, Phoenix and HyperDX ports forward over the tailnet. Funnel stays off.
func Render(observability bool) ([]byte, error) {
	cfg := serveConfig{
		TCP: map[string]tcpHandler{"443": {HTTPS: true}},
		Web: map[string]webServer{
			certDomain: {Handlers: map[string]webHandler{"/": {Proxy: "http://gateway:1320"}}},
		},
		AllowFunnel: map[string]bool{certDomain: false},
	}
	if observability {
		for port, target := range observabilityForwards {
			cfg.TCP[port] = tcpHandler{TCPForward: target}
		}
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("tsserve: render: %w", err)
	}
	return append(out, '\n'), nil
}

// Write renders serve.json into dir (created if missing) and returns its path.
func Write(dir string, observability bool) (string, error) {
	data, err := Render(observability)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("tsserve: mkdir %q: %w", dir, err)
	}
	path := filepath.Join(dir, "serve.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("tsserve: write %q: %w", path, err)
	}
	return path, nil
}
