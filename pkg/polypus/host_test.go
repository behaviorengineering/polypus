package polypus_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/pkg/polypus"
)

// hostConfig is a one-backend gateway YAML, as a host would ship it.
const hostConfig = `backends:
  local:
    base_url: http://127.0.0.1:9/v1
    capabilities: [chat]
`

// hostEnv keeps the gateway off the network and out of the user's state.
func hostEnv(t *testing.T) polypus.ServeOptions {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(hostConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	t.Setenv("POLYPUS_MODELS_WARMUP", "0")
	t.Setenv("POLYPUS_MODELS_REFRESH_INTERVAL", "off")
	t.Setenv("POLYPUS_BATCH_DIR", filepath.Join(dir, "batches"))
	return polypus.ServeOptions{Host: "127.0.0.1", Port: 1, ConfigPath: path}
}

func TestHostBuildsHandlerThroughFacade(t *testing.T) {
	opts := hostEnv(t)
	h, err := polypus.NewHandler(opts,
		polypus.WithAdminStateDir(t.TempDir()),
		polypus.WithObservability("", nil))
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	defer func() {
		// The server stops first, then the handler releases its router.
		srv.Close()
		if err := h.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
		if err := h.Close(); err != nil {
			t.Errorf("a second Close must be safe: %v", err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("/health: status %d body %s", resp.StatusCode, body)
	}
}

// fakeGateway answers /health and one chat completion, as a running gateway would.
func fakeGateway() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	})
	return mux
}

func TestHostSmokesThroughFacade(t *testing.T) {
	srv := httptest.NewServer(fakeGateway())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	results, err := polypus.Smoke(ctx, polypus.SmokeOptions{
		BaseURL:   srv.URL,
		Channels:  []string{"chat"},
		ChatModel: "local/m",
		MaxTokens: 8,
	})
	if err != nil {
		t.Fatalf("Smoke: %v (results %+v)", err, results)
	}
	var passed int
	for _, r := range results {
		if r.Channel != "chat" {
			t.Fatalf("unexpected channel %q", r.Channel)
		}
		if r.Probe == "content_nonempty" && r.Status == "pass" {
			passed++
		}
	}
	if passed != 1 {
		t.Fatalf("want one passing chat completion probe, got %+v", results)
	}
}

// ExampleNewHandler embeds the gateway in a host's own HTTP server. The host
// owns the listener and its timeouts; NewHandler only builds the handler. The
// example has no Output line, so go test compiles it without running it against
// the machine's own config.
func ExampleNewHandler() {
	opts, err := polypus.LoadServeOptions()
	if err != nil {
		fmt.Println("serve options:", polypus.CodeOf(err))
		return
	}
	h, err := polypus.NewHandler(opts, polypus.WithObservability("", nil))
	if err != nil {
		fmt.Println("handler:", polypus.CodeOf(err))
		return
	}
	// Close releases the handler's router once the server has shut down.
	defer func() { _ = h.Close() }()
	mux := http.NewServeMux()
	mux.Handle("/", h)
	srv := &http.Server{
		Addr:              net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port)),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	// The host serves and shuts down on its own signal, for example:
	//   go srv.ListenAndServe()
	//   <-ctx.Done()
	//   srv.Shutdown(shutdownCtx)
	//   h.Close()
	fmt.Println("listening on", srv.Addr)
}
