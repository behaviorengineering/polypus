// Package tsserve renders the Tailscale serve config for a deploy: HTTPS on 443 to
// the gateway, and the observability ports forwarded only when those services run.
package tsserve

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/behaviorengineering/polypus/internal/deploy/stack"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// certDomain is expanded by the Tailscale container to the node's own name.
const certDomain = "${TS_CERT_DOMAIN}:443"

// observabilityForwards are the tailnet ports forwarded to the collector, Phoenix
// and HyperDX inside the compose network.
var observabilityForwards = map[string]string{
	"4317": stack.ServiceOtelcol + ":4317",
	"4318": stack.ServiceOtelcol + ":4318",
	"6006": stack.ServicePhoenix + ":6006",
	"8080": stack.ServiceHyperDX + ":8080",
}

// gatewayProxy is the gateway's address inside the compose network.
const gatewayProxy = "http://" + stack.ServiceGateway + ":1320" // NOSONAR: compose-network hop; Tailscale serve terminates HTTPS in front

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

// render returns serve.json: 443 proxies to the gateway, and with observability the
// OTLP, Phoenix and HyperDX ports forward over the tailnet. Funnel stays off.
func render(observability bool) ([]byte, error) {
	cfg := serveConfig{
		TCP: map[string]tcpHandler{"443": {HTTPS: true}},
		Web: map[string]webServer{
			certDomain: {Handlers: map[string]webHandler{"/": {Proxy: gatewayProxy}}},
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
		return nil, derrors.Wrap(err, derrors.CodeInternal, "tsserve.render", "marshal serve.json")
	}
	return append(out, '\n'), nil
}

// Write renders serve.json into dir (created if missing) and returns its path.
func Write(dir string, observability bool) (string, error) {
	const op = "tsserve.Write"
	data, err := render(observability)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", derrors.Wrap(err, derrors.CodeInternal, op, "mkdir "+dir)
	}
	path := filepath.Join(dir, "serve.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", derrors.Wrap(err, derrors.CodeInternal, op, "write "+path)
	}
	return path, nil
}
