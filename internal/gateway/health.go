package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
)

const backendProbeTimeout = 5 * time.Second

// healthResponse is the keyless liveness body. It names no backend, URL or account:
// those sit behind the client key on /health/backends and /health/upstreams.
type healthResponse struct {
	Status string `json:"status"`
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

type upstreamHealthResponse struct {
	Status    string                      `json:"status"`
	Upstreams []upstream.UpstreamSnapshot `json:"upstreams"`
}

// serveUpstreamHealth reports in-process circuit breaker state (no upstream dials).
func (h healthHandler) serveUpstreamHealth(w http.ResponseWriter, _ *http.Request) {
	resp := upstreamHealthResponse{
		Status:    "ok",
		Upstreams: h.upstreams.Snapshot(),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	h.encodeJSON(w, resp)
}

// serveHealth reports gateway liveness only (no upstream probes).
func (h healthHandler) serveHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	h.encodeJSON(w, healthResponse{Status: "ok"})
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
		if err := h.probeBackend(ctx, b); err != nil {
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
		if err := h.compat.CheckHealth(ctx, strings.TrimRight(strings.TrimSpace(switchyardURL), "/")+"/health"); err != nil {
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
	h.encodeJSON(w, backendHealthResponse{
		Status:   status,
		Router:   "bifrost",
		Backends: backends,
	})
}

// probeRemoteCredentials validates each remote backend before serve accepts traffic.
func probeRemoteCredentials(ctx context.Context, cfg config.RouterConfig, getCF CloudflareClientGet) error {
	for _, id := range cfg.BackendIDs() {
		b := cfg.Backends[id]
		if !b.Remote {
			continue
		}
		if err := probeRemoteBackend(ctx, b, getCF); err != nil {
			return fmt.Errorf("startup probe backend %s: %w (check polypus secret set / env)", id, err)
		}
	}
	return nil
}

// probeBackend checks one backend for the health page: a remote one by its
// credential (and a Cloudflare ping), a local one by a GET of its root.
func (s *shared) probeBackend(ctx context.Context, b config.BackendDef) error {
	if b.Remote {
		return probeRemoteBackend(ctx, b, s.cloudflareClient)
	}
	if err := s.compat.Ping(ctx, strings.TrimRight(b.BaseURL, "/")+"/"); err != nil {
		return gatewayErr(err, "gateway.probeBackend", "ping").With("backend", b.ID)
	}
	return nil
}

// probeRemoteBackend checks a remote backend's credential; a Cloudflare backend is
// also pinged.
func probeRemoteBackend(ctx context.Context, b config.BackendDef, getCF CloudflareClientGet) error {
	if b.IsCloudflareExtension() {
		if getCF == nil {
			getCF = new(cloudflare.Clients).Get
		}
		cf, err := getCF(b)
		if err != nil {
			return gatewayErr(err, "gateway.probeBackend", "cloudflare client").With("backend", b.ID)
		}
		if err := cf.Ping(ctx); err != nil {
			return gatewayErr(err, "gateway.probeBackend", "ping").With("backend", b.ID)
		}
		return nil
	}
	if _, err := b.Auth.ResolveBearerToken(); err != nil {
		return derrors.Wrap(err, derrors.CodeNotReady, "gateway.probeBackend", "backend credential").With("backend", b.ID)
	}
	return nil
}
