//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/smoke"
)

const modulePath = "github.com/behaviorengineering/polypus"

// Options configures harness startup.
type Options struct {
	Live bool
}

// Harness is a running polypus gateway for integration smoke.
type Harness struct {
	BaseURL string
	Opts    Options

	binPath  string
	cmd      *exec.Cmd
	mock     *httptest.Server
	childEnv []string

	tmpRoot string
	logBuf  bytes.Buffer
	logMu   sync.Mutex
}

var (
	sharedHarness *Harness
	sharedOnce    sync.Once
	sharedErr     error
)

// LiveFromEnv reports whether POLYPUS_SMOKE_LIVE=1.
func LiveFromEnv() bool {
	return strings.TrimSpace(os.Getenv("POLYPUS_SMOKE_LIVE")) == "1"
}

// Shared returns the package-level harness started from TestMain.
func Shared() (*Harness, error) {
	return sharedHarness, sharedErr
}

// StartShared boots the gateway once for the integration package.
func StartShared(opts Options) error {
	sharedOnce.Do(func() {
		sharedHarness, sharedErr = startHarness(opts)
	})
	return sharedErr
}

// Start boots a dedicated gateway (for tests that need isolation).
func Start(tb testing.TB, opts Options) *Harness {
	h, err := startHarness(opts)
	if err != nil {
		tb.Fatalf("integration harness: %v", err)
	}
	tb.Cleanup(h.Stop)
	return h
}

func startHarness(opts Options) (*Harness, error) {
	root, err := moduleRoot()
	if err != nil {
		return nil, err
	}
	tmpRoot, err := os.MkdirTemp("", "polypus-integration-")
	if err != nil {
		return nil, fmt.Errorf("temp dir: %w", err)
	}

	h := &Harness{Opts: opts, tmpRoot: tmpRoot}
	if opts.Live {
		if err := h.setupLive(); err != nil {
			h.Stop()
			return nil, err
		}
	} else if err := h.setupHermetic(); err != nil {
		h.Stop()
		return nil, err
	}

	port, err := freePort()
	if err != nil {
		h.Stop()
		return nil, err
	}
	h.BaseURL = fmt.Sprintf("http://127.0.0.1:%d", port)

	bin := filepath.Join(tmpRoot, "polypus")
	if err := buildGateway(root, bin); err != nil {
		h.Stop()
		return nil, err
	}
	h.binPath = bin

	if err := h.startServe(port); err != nil {
		h.Stop()
		return nil, err
	}
	deadline := 30 * time.Second
	if opts.Live {
		deadline = 120 * time.Second
	}
	if err := waitHealth(h.BaseURL, deadline); err != nil {
		h.Stop()
		return nil, fmt.Errorf("%w\n--- gateway log ---\n%s", err, h.gatewayLog())
	}
	return h, nil
}

func (h *Harness) setupHermetic() error {
	h.mock = startMockCloudflare()
	cfV1 := strings.TrimRight(h.mock.URL, "/") + "/client/v4/accounts/test/ai/v1"
	configPath := filepath.Join(h.tmpRoot, "config.yaml")
	if err := writeHermeticConfig(configPath, cfV1); err != nil {
		return err
	}
	return h.prepareChildEnv(configPath)
}

func (h *Harness) setupLive() error {
	key := strings.TrimSpace(os.Getenv("CF_AI_API_KEY"))
	acct := strings.TrimSpace(os.Getenv("CF_ACCOUNT_ID"))
	if key == "" || acct == "" {
		return fmt.Errorf("live smoke requires CF_AI_API_KEY and CF_ACCOUNT_ID")
	}
	configPath := filepath.Join(h.tmpRoot, "config.yaml")
	if err := writeLiveConfig(configPath, acct); err != nil {
		return err
	}
	return h.prepareChildEnv(configPath)
}

func (h *Harness) prepareChildEnv(configPath string) error {
	home := filepath.Join(h.tmpRoot, "home")
	for _, sub := range []string{"batch"} {
		if err := os.MkdirAll(filepath.Join(h.tmpRoot, sub), 0o755); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	h.childEnv = childEnv(configPath, h.tmpRoot, home)
	return nil
}

func childEnv(configPath, tmpRoot, home string) []string {
	overrides := map[string]string{
		"POLYPUS_CONFIG":     configPath,
		"POLYPUS_BATCH_DIR":  filepath.Join(tmpRoot, "batch"),
		"HOME":               home,
		"XDG_CONFIG_HOME":    filepath.Join(home, ".config"),
		"XDG_STATE_HOME":     filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME":     filepath.Join(home, ".cache"),
		"POLYPUS_OTEL":       "0",
		"POLYPUS_PHOENIX":    "0",
		"POLYPUS_HYPERDX":    "0",
		"POLYPUS_SWITCHYARD": "0",
		"CF_AI_API_KEY":      envOr("CF_AI_API_KEY", "test-token"),
		"CF_ACCOUNT_ID":      envOr("CF_ACCOUNT_ID", "test"),
	}
	out := make([]string, 0, len(os.Environ())+len(overrides))
	for _, kv := range os.Environ() {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if _, ok := overrides[key]; ok {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func (h *Harness) startServe(port int) error {
	h.cmd = exec.Command(h.binPath, "serve", "--host", "127.0.0.1", "--port", fmt.Sprintf("%d", port))
	h.cmd.Env = h.childEnv
	stdout, err := h.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := h.cmd.StderrPipe()
	if err != nil {
		return err
	}
	go h.copyLog(stdout)
	go h.copyLog(stderr)
	if runtime.GOOS != "windows" {
		h.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := h.cmd.Start(); err != nil {
		return fmt.Errorf("start serve: %w", err)
	}
	return nil
}

func (h *Harness) copyLog(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			h.logMu.Lock()
			h.logBuf.Write(buf[:n])
			h.logMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (h *Harness) gatewayLog() string {
	h.logMu.Lock()
	defer h.logMu.Unlock()
	const max = 64 << 10
	data := h.logBuf.Bytes()
	if len(data) > max {
		data = data[len(data)-max:]
	}
	return string(data)
}

// Stop tears down the gateway and temp resources.
func (h *Harness) Stop() {
	if h == nil {
		return
	}
	if h.cmd != nil && h.cmd.Process != nil {
		if runtime.GOOS != "windows" {
			_ = syscall.Kill(-h.cmd.Process.Pid, syscall.SIGTERM)
		} else {
			_ = h.cmd.Process.Signal(os.Interrupt)
		}
		done := make(chan error, 1)
		go func() { done <- h.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = h.cmd.Process.Kill()
			<-done
		}
	}
	if h.mock != nil {
		h.mock.Close()
		h.mock = nil
	}
	h.cleanupDirs()
}

func (h *Harness) cleanupDirs() {
	if h == nil || h.tmpRoot == "" {
		return
	}
	if os.Getenv("POLYPUS_SMOKE_KEEP") == "1" {
		return
	}
	_ = os.RemoveAll(h.tmpRoot)
	h.tmpRoot = ""
}

// SmokeOpts builds smoke.Options for the running gateway.
func (h *Harness) SmokeOpts(channels []string) smoke.Options {
	opts := smoke.Options{
		BaseURL:   h.BaseURL,
		Channels:  channels,
		RequireCF: h.Opts.Live,
	}
	opts.Normalize()
	return opts
}

// RunSmoke executes smoke.Run with an appropriate timeout.
func (h *Harness) RunSmoke(ctx context.Context, channels []string) ([]smoke.Result, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, fmt.Errorf("integration: context deadline required")
	}
	return smoke.Run(ctx, h.SmokeOpts(channels))
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
			if err != nil {
				return "", err
			}
			if strings.Contains(string(data), modulePath) {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("integration: module root not found for %s", modulePath)
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = ln.Close() }()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("integration: unexpected listen addr")
	}
	return addr.Port, nil
}

func buildGateway(moduleRoot, outPath string) error {
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", outPath, "./cmd/polypus")
	cmd.Dir = moduleRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go build: %w: %s", err, string(out))
	}
	return nil
}

func waitHealth(baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	healthURL := strings.TrimRight(baseURL, "/") + "/health"
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, healthURL, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			code := resp.StatusCode
			_ = resp.Body.Close()
			if code >= 200 && code < 300 {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("integration: gateway not healthy within %s", timeout)
}
