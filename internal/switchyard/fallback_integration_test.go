//go:build integration

package switchyard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
)

// TestFallbackRouteFailsOverOnFirstTry runs the real switchyard-server against a fake
// Polypus: the first target answers 503, so the route must answer from the second at once.
// Build bin/switchyard-server first with go tool task build.
func TestFallbackRouteFailsOverOnFirstTry(t *testing.T) {
	if _, err := os.Stat(switchyardBin); err != nil {
		t.Skipf("switchyard-server not built: %v", err)
	}

	var primaryCalls, secondaryCalls atomic.Int32
	polypus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.HasPrefix(req.Model, "claude_team/") {
			primaryCalls.Add(1)
			w.Header().Set("Retry-After", "30")
			http.Error(w, `{"error":{"message":"out of credit"}}`, http.StatusServiceUnavailable)
			return
		}
		secondaryCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"x","object":"chat.completion","created":1,"model":%q,`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"from openrouter"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, req.Model)
	}))
	t.Cleanup(polypus.Close)

	toml, err := Render(config.RouterConfig{
		Routers: map[string]config.NamedRouter{
			"turno": {
				Name:       "turno",
				Capability: config.CapChat,
				Route: config.RouterRoute{
					Type:            config.RouteFallback,
					FallbackTargets: []string{"claude_team/claude-sonnet-5-5", "openrouter/anthropic/claude-sonnet-5.5"},
				},
			},
		},
	}, polypus.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "routes.toml")
	if err := os.WriteFile(cfgPath, toml, 0o600); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// The binary path is a constant; the arguments are this test's temp config path and port.
	cmd := exec.CommandContext(ctx, switchyardBin, "--config", cfgPath, "--host", "127.0.0.1", "--port", fmt.Sprint(port)) //nolint:gosec // G204: test-owned arguments
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitHealthy(ctx, t, base+"/health", &logs)

	start := time.Now()
	body := `{"model":"router/turno","messages":[{"role":"user","content":"hi"}]}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), "from openrouter") {
		t.Fatalf("status %d body %s\nswitchyard logs:\n%s", resp.StatusCode, raw, logs.String())
	}
	if got := resp.Header.Get("x-model-router-selected-model"); !strings.Contains(got, "openrouter/") {
		t.Fatalf("selected model header: %q", got)
	}
	if primaryCalls.Load() != 1 || secondaryCalls.Load() != 1 {
		t.Fatalf("calls: primary=%d secondary=%d (want 1 and 1: no retries)", primaryCalls.Load(), secondaryCalls.Load())
	}
	if elapsed > 5*time.Second {
		t.Fatalf("fallback took %s; it must not wait on Retry-After", elapsed)
	}
}

// switchyardBin is where go tool task build installs the server, relative to this package.
const switchyardBin = "../../bin/switchyard-server"

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func waitHealthy(ctx context.Context, t *testing.T, url string, logs *bytes.Buffer) {
	t.Helper()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("switchyard never became healthy:\n%s", logs.String())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
