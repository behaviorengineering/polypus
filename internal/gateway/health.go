package gateway

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
)

// backendProbeTimeout is the production budget for one round of backend probes.
const backendProbeTimeout = 5 * time.Second

// healthResponse is the keyless liveness body. It names no backend, URL or account:
// those sit behind the client key on /health/backends and /health/upstreams.
type healthResponse struct {
	Status string `json:"status"`
}

type backendProbeResult struct {
	ID  string `json:"id"`
	URL string `json:"url"`
	OK  bool   `json:"ok"`
	// Code and Error are the failure's domain code and public message; the full
	// error chain goes to the log only.
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}

type backendHealthResponse struct {
	Status   string               `json:"status"`
	Router   string               `json:"router"`
	Backends []backendProbeResult `json:"backends"`
}

type upstreamHealthResponse struct {
	Status    string              `json:"status"`
	Upstreams []upstream.Snapshot `json:"upstreams"`
}

// serveUpstreamHealth reports in-process circuit breaker state (no upstream dials).
func (h healthHandler) serveUpstreamHealth(w http.ResponseWriter, _ *http.Request) {
	resp := upstreamHealthResponse{
		Status:    "ok",
		Upstreams: h.upstreams.Snapshot(),
	}
	h.writeJSON(w, http.StatusOK, resp)
}

// serveHealth reports gateway liveness only (no upstream probes).
func (h healthHandler) serveHealth(w http.ResponseWriter, _ *http.Request) {
	h.writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

// serveBackendHealth probes configured upstream backends (manual / stack-doctor use).
// Every probe runs at once under one shared budget, so a slow backend costs the
// page that backend's wait, not the sum of all of them.
func (h healthHandler) serveBackendHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.probeTimeout)
	defer cancel()

	cfg := h.registry.Config()
	ids := cfg.BackendIDs()
	probes := make([]func() (backendProbeResult, error), 0, len(ids)+1)
	for _, id := range ids {
		b := cfg.Backends[id]
		probes = append(probes, func() (backendProbeResult, error) {
			// Probes must not trip the same circuit breaker as chat/TTS/STT dials.
			return backendProbeResult{ID: id, URL: b.BaseURL}, h.probeBackend(ctx, b)
		})
	}
	if h.switchyardEnabled {
		switchyardURL := cfg.EffectiveSwitchyardBaseURL()
		probes = append(probes, func() (backendProbeResult, error) {
			healthURL := strings.TrimRight(strings.TrimSpace(switchyardURL), "/") + "/health"
			return backendProbeResult{ID: "switchyard", URL: switchyardURL}, h.compat.CheckHealth(ctx, healthURL)
		})
	}

	backends := make([]backendProbeResult, len(probes))
	var wg sync.WaitGroup
	for i, probe := range probes {
		wg.Go(func() {
			entry, err := probe()
			if err != nil {
				h.log.Warn("polypus: backend health probe failed", "backend", entry.ID, "err", err)
				entry.Code = string(derrors.CodeOf(err))
				entry.Error = derrors.PublicMessage(err)
			} else {
				entry.OK = true
			}
			backends[i] = entry
		})
	}
	wg.Wait()

	status := "ok"
	code := http.StatusOK
	for _, entry := range backends {
		if !entry.OK {
			status = "degraded"
			code = http.StatusServiceUnavailable
			break
		}
	}
	h.writeJSON(w, code, backendHealthResponse{
		Status:   status,
		Router:   "bifrost",
		Backends: backends,
	})
}

// probeRemoteCredentials validates each remote backend before serve accepts traffic.
func probeRemoteCredentials(ctx context.Context, cfg config.RouterConfig, getCF cloudflareClientGet) error {
	for _, id := range cfg.BackendIDs() {
		b := cfg.Backends[id]
		if !b.Remote {
			continue
		}
		if err := probeRemoteBackend(ctx, b, getCF); err != nil {
			return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeNotReady), "gateway.probeRemoteCredentials",
				"startup probe of backend "+id+" failed (check polypus secret set / env)").With("backend", id)
		}
	}
	return nil
}

// probeBackend checks one backend for the health page: a remote one by its
// credential (and a Cloudflare ping), a local one by a GET of its root.
func (h healthHandler) probeBackend(ctx context.Context, b config.BackendDef) error {
	if b.Remote {
		return probeRemoteBackend(ctx, b, h.clients.cf)
	}
	if err := h.compat.Ping(ctx, strings.TrimRight(b.BaseURL, "/")+"/"); err != nil {
		return gatewayErr(err, "gateway.probeBackend", "ping").With("backend", b.ID)
	}
	return nil
}

// probeRemoteBackend checks a remote backend's credential; a Cloudflare backend is
// also pinged through getCF, the gateway's own client getter.
func probeRemoteBackend(ctx context.Context, b config.BackendDef, getCF cloudflareClientGet) error {
	const op = "gateway.probeRemoteBackend"
	if b.IsCloudflareExtension() {
		if getCF == nil {
			return derrors.New(derrors.CodeInternal, op, "cloudflare client getter required").With("backend", b.ID)
		}
		cf, err := getCF(b)
		if err != nil {
			return gatewayErr(err, op, "cloudflare client").With("backend", b.ID)
		}
		if err := cf.Ping(ctx); err != nil {
			return gatewayErr(err, op, "ping").With("backend", b.ID)
		}
		return nil
	}
	if _, err := b.Auth.ResolveBearerToken(); err != nil {
		return derrors.Wrap(err, derrors.CodeNotReady, op, "backend credential").With("backend", b.ID)
	}
	return nil
}
