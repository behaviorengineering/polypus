package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogStartupRedactsBackendURL(t *testing.T) {
	cfg := RouterConfig{Backends: map[string]BackendDef{
		"private": {ID: "private", BaseURL: "https://user:s3cret@host.example/v1?key=k3y", Capabilities: []Capability{CapChat}},
		"public":  {ID: "public", BaseURL: "http://127.0.0.1:1234/v1", Capabilities: []Capability{CapChat}},
	}}
	var buf bytes.Buffer
	cfg.LogStartup(slog.New(slog.NewTextHandler(&buf, nil)))
	out := buf.String()
	for _, secret := range []string{"s3cret", "user:", "k3y"} {
		if strings.Contains(out, secret) {
			t.Fatalf("startup log leaked %q:\n%s", secret, out)
		}
	}
	for _, want := range []string{"host.example/v1", "http://127.0.0.1:1234/v1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("startup log lost %q:\n%s", want, out)
		}
	}
}

func TestBackendBaseURLErrorsDoNotLeakCredentials(t *testing.T) {
	for name, baseURL := range map[string]string{
		"no host":     "https://user:s3cret@/v1",
		"unparseable": "http://user:s3cret@[::1",
		"no scheme":   "user:s3cret@host.example",
	} {
		t.Run(name, func(t *testing.T) {
			content := "backends:\n  b:\n    base_url: \"" + baseURL + "\"\n    capabilities: [chat]\n"
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadRouterConfig(ServeOptions{ConfigPath: path})
			if err == nil {
				t.Fatal("a base_url without a usable scheme and host must fail")
			}
			if strings.Contains(err.Error(), "s3cret") {
				t.Fatalf("error leaked the credential: %v", err)
			}
		})
	}
}
