package tsserve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func decode(t *testing.T, observability bool) serveConfig {
	t.Helper()
	data, err := Render(observability)
	if err != nil {
		t.Fatal(err)
	}
	var cfg serveConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("serve.json does not parse: %v\n%s", err, data)
	}
	return cfg
}

func TestRenderWithoutObservability(t *testing.T) {
	cfg := decode(t, false)
	if len(cfg.TCP) != 1 || !cfg.TCP["443"].HTTPS {
		t.Fatalf("TCP should hold only HTTPS 443: %+v", cfg.TCP)
	}
	if got := cfg.Web[certDomain].Handlers["/"].Proxy; got != "http://gateway:1320" {
		t.Fatalf("proxy %q", got)
	}
	if funnel, ok := cfg.AllowFunnel[certDomain]; !ok || funnel {
		t.Fatalf("funnel must be present and off: %+v", cfg.AllowFunnel)
	}
}

func TestRenderWithObservability(t *testing.T) {
	cfg := decode(t, true)
	want := map[string]string{"4317": "otelcol:4317", "4318": "otelcol:4318", "6006": "phoenix:6006", "8080": "hyperdx:8080"}
	for port, target := range want {
		if got := cfg.TCP[port].TCPForward; got != target {
			t.Fatalf("port %s forwards to %q want %q", port, got, target)
		}
	}
	if !cfg.TCP["443"].HTTPS {
		t.Fatal("443 must stay HTTPS")
	}
}

func TestWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tailscale")
	path, err := Write(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "serve.json") {
		t.Fatalf("path %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
