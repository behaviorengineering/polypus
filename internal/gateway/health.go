package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	"github.com/behaviorengineering/polypus/internal/config"
)

const backendProbeTimeout = 5 * time.Second

type healthBackend struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type healthSwitchyard struct {
	URL string `json:"url"`
}

type healthResponse struct {
	Status     string            `json:"status"`
	Router     string            `json:"router"`
	Switchyard *healthSwitchyard `json:"switchyard,omitempty"`
	Backends   []healthBackend   `json:"backends"`
}

type backendProbeResult struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type backendHealthResponse struct {
	Status   string               `json:"status"`
	Router   string               `json:"router"`
	Backends []backendProbeResult `json:"backends"`
}

// serveHealth reports gateway liveness only (no upstream probes).
func (h healthHandler) serveHealth(w http.ResponseWriter, _ *http.Request) {
	reg := h.router.Registry()
	cfg := reg.Config()
	backends := make([]healthBackend, 0, len(cfg.Backends))
	for _, id := range cfg.BackendIDs() {
		b := cfg.Backends[id]
		backends = append(backends, healthBackend{ID: id, URL: b.BaseURL})
	}
	resp := healthResponse{
		Status:   "ok",
		Router:   "bifrost",
		Backends: backends,
	}
	if config.SwitchyardEnabled() {
		resp.Switchyard = &healthSwitchyard{
			URL: cfg.EffectiveSwitchyardBaseURL(),
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// serveBackendHealth probes configured upstream backends (manual / stack-doctor use).
func (h healthHandler) serveBackendHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), backendProbeTimeout)
	defer cancel()

	reg := h.router.Registry()
	cfg := reg.Config()
	backends := make([]backendProbeResult, 0, len(cfg.Backends))
	allOK := true
	for _, id := range cfg.BackendIDs() {
		b := cfg.Backends[id]
		entry := backendProbeResult{ID: id, URL: b.BaseURL}
		// Probes must not trip the same circuit breaker as chat/TTS/STT dials.
		if err := probeBackend(ctx, b, h.cloudflareClient); err != nil {
			entry.Error = err.Error()
			allOK = false
		} else {
			entry.OK = true
		}
		backends = append(backends, entry)
	}

	if config.SwitchyardEnabled() {
		switchyardURL := cfg.EffectiveSwitchyardBaseURL()
		entry := backendProbeResult{ID: "switchyard", URL: switchyardURL}
		if err := probeSwitchyard(ctx, switchyardURL); err != nil {
			entry.Error = err.Error()
			allOK = false
		} else {
			entry.OK = true
		}
		backends = append(backends, entry)
	}

	status := "ok"
	code := http.StatusOK
	if !allOK {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(backendHealthResponse{
		Status:   status,
		Router:   "bifrost",
		Backends: backends,
	})
}

// probeCloudflareCredentials pings each remote Cloudflare extension backend before serve accepts traffic.
func probeCloudflareCredentials(ctx context.Context, cfg config.RouterConfig, getCF CloudflareClientGet) error {
	for _, id := range cfg.BackendIDs() {
		b := cfg.Backends[id]
		if !b.Remote || !b.IsCloudflareExtension() {
			continue
		}
		if err := probeBackend(ctx, b, getCF); err != nil {
			return fmt.Errorf("startup probe backend %s: %w (check polypus secret set / env)", id, err)
		}
	}
	return nil
}

func probeBackend(ctx context.Context, b config.BackendDef, getCF CloudflareClientGet) error {
	if b.Remote {
		if b.IsCloudflareExtension() {
			if getCF == nil {
				getCF = cloudflare.GetClient
			}
			cf, err := getCF(b)
			if err != nil {
				return err
			}
			return cf.Ping(ctx)
		}
		if _, err := b.Auth.ResolveBearerToken(); err != nil {
			return err
		}
		return nil
	}
	return pingBackendURL(ctx, b.BaseURL)
}

func pingBackendURL(ctx context.Context, backendURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(backendURL, "/")+"/", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: backendProbeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 500 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
