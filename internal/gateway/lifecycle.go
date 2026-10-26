package gateway

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/behaviorengineering/polypus/internal/switchyard"
)

// Close releases owned router resources (no-op when WithRouter injected the Router).
func (g *Gateway) Close() {
	if g == nil || g.shared == nil || !g.ownedRouter {
		return
	}
	if c, ok := g.router.(routerCloser); ok {
		c.Close()
	}
}

// ListenAndServe writes Switchyard routes if needed, then serves on opts.ListenAddr().
func ListenAndServe(opts config.ServeOptions) error {
	handler, err := NewHandler(opts)
	if err != nil {
		return err
	}
	g, ok := gatewayFromHandler(handler)
	if !ok {
		if _, wrapped := handler.(*accessProtector); wrapped {
			return fmt.Errorf("gateway: access-protected handler could not be unwrapped (internal wiring error)")
		}
		return fmt.Errorf("gateway: unexpected handler type %T", handler)
	}
	defer func() {
		if c, ok := handler.(interface{ Close() }); ok {
			c.Close()
			return
		}
		g.Close()
	}()

	if err := writeSwitchyardConfig(g, opts); err != nil {
		return err
	}

	probeCtx, probeCancel := context.WithTimeout(context.Background(), backendProbeTimeout)
	defer probeCancel()
	if err := probeRemoteCredentials(probeCtx, g.router.Registry().Config(), g.cfGet); err != nil {
		return fmt.Errorf("gateway: %w", err)
	}

	refreshCtx, refreshCancel := context.WithCancel(context.Background())
	defer refreshCancel()

	if modelsWarmupEnabled() {
		cfg := g.router.Registry().Config()
		warmCtx, warmCancel := context.WithTimeout(context.Background(), modelsWarmupTimeout(cfg))
		defer warmCancel()
		modelsHandler{g.shared}.warmModelsInventory(warmCtx)
		modelsHandler{g.shared}.startModelsRefreshLoop(refreshCtx, cfg)
	}

	// No WriteTimeout: SSE chat streams outlive any fixed write budget; the hop
	// budget and the stream idle limit bound them instead.
	server := &http.Server{
		Addr:              opts.ListenAddr(),
		Handler:           observability.WrapHandler(handler),
		ReadHeaderTimeout: 30 * time.Second,
		ReadTimeout:       serverReadTimeout,
		IdleTimeout:       serverIdleTimeout,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-sigCh:
		refreshCancel()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			return err
		}
		return nil
	}
}

// writeSwitchyardConfig renders Switchyard TOML from the live router config (process startup).
func writeSwitchyardConfig(g *Gateway, opts config.ServeOptions) error {
	if g == nil || g.router == nil {
		return fmt.Errorf("gateway: router not configured")
	}
	if _, err := switchyard.WriteConfigIfNeeded(g.router.Registry().Config(), opts.GatewayBaseURL()); err != nil {
		return fmt.Errorf("gateway: switchyard render: %w", err)
	}
	return nil
}

const (
	// serverReadTimeout bounds reading one request, headers and body: a 26 MiB
	// audio upload on a slow link fits; a client that stalls mid-body does not.
	serverReadTimeout = 5 * time.Minute
	// serverIdleTimeout reaps idle keep-alive connections.
	serverIdleTimeout = 2 * time.Minute
)
