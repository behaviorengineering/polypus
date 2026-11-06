package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/behaviorengineering/polypus/internal/switchyard"
)

// Close releases owned router resources (no-op when withRouter injected the router).
func (g *gatewayHandler) Close() {
	if g == nil || g.shared == nil || g.closeRouter == nil {
		return
	}
	g.closeRouter()
}

// ListenAndServe prepares the gateway (Switchyard routes, credential probes, models
// warmup), then serves on opts.ListenAddr() until ctx is done, and shuts down
// gracefully. The caller owns process signals (signal.NotifyContext).
func ListenAndServe(ctx context.Context, opts config.ServeOptions, options ...HandlerOption) error {
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, "gateway.ListenAndServe", "context is required")
	}
	handler, g, err := newServingHandler(opts, options...)
	if err != nil {
		return err
	}
	defer g.Close()
	// The refresh loop runs for the life of the server and stops with it, before the
	// router closes: cancel it, then wait for an in-flight refresh to finish.
	refreshCtx, refreshCancel := context.WithCancel(ctx)
	waitRefresh := func() {}
	defer func() {
		refreshCancel()
		waitRefresh()
	}()
	waitRefresh, err = prepare(ctx, refreshCtx, g, opts)
	if err != nil {
		return err
	}
	// No WriteTimeout: SSE chat streams outlive any fixed write budget; the hop
	// budget and the stream idle limit bound them instead.
	return serve(ctx, &http.Server{
		Addr:              opts.ListenAddr(),
		Handler:           observability.WrapHandler(handler, g.traceSkipPaths),
		ReadHeaderTimeout: 30 * time.Second,
		ReadTimeout:       serverReadTimeout,
		IdleTimeout:       serverIdleTimeout,
	})
}

// newServingHandler builds the public handler and the gateway behind it.
func newServingHandler(opts config.ServeOptions, options ...HandlerOption) (http.Handler, *gatewayHandler, error) {
	handler, err := NewHandler(opts, options...)
	if err != nil {
		return nil, nil, err
	}
	g, ok := gatewayFromHandler(handler)
	if !ok {
		return nil, nil, derrors.New(derrors.CodeInternal, "gateway.ListenAndServe", "unexpected handler type").
			With("type", fmt.Sprintf("%T", handler))
	}
	return handler, g, nil
}

// prepare does the startup work before the server accepts traffic: it writes the
// Switchyard routes, probes remote credentials, warms the models inventory and starts
// its refresh loop on refreshCtx. wait blocks until that loop has stopped; it is never
// nil.
func prepare(ctx, refreshCtx context.Context, g *gatewayHandler, opts config.ServeOptions) (wait func(), err error) {
	wait = func() {}
	if err := writeSwitchyardConfig(g, opts); err != nil {
		return wait, err
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, backendProbeTimeout)
	defer probeCancel()
	if err := probeRemoteCredentials(probeCtx, g.registry.Config(), g.cfGet); err != nil {
		return wait, err
	}
	if g.models.Warmup {
		cfg := g.registry.Config()
		warmCtx, warmCancel := context.WithTimeout(ctx, modelsWarmupTimeout(cfg))
		defer warmCancel()
		modelsHandler{g.shared}.warmInventory(warmCtx, false)
		wait = modelsHandler{g.shared}.startModelsRefreshLoop(refreshCtx, cfg)
	}
	return wait, nil
}

// serve runs server until it fails or ctx is done, then shuts it down within
// shutdownTimeout.
func serve(ctx context.Context, server *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return derrors.Wrap(err, derrors.CodeUnavailable, "gateway.serve", "listen").With("addr", server.Addr)
	case <-ctx.Done():
		// ctx is already done, so the shutdown budget hangs off a detached copy.
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return derrors.Wrap(err, derrors.CodeInternal, "gateway.serve", "shutdown")
		}
		return nil
	}
}

// writeSwitchyardConfig renders Switchyard TOML from the live router config (process startup).
func writeSwitchyardConfig(g *gatewayHandler, opts config.ServeOptions) error {
	const op = "gateway.writeSwitchyardConfig"
	if g == nil || g.shared == nil || g.registry == nil {
		return derrors.New(derrors.CodeNotReady, op, "router not configured")
	}
	if _, err := switchyard.WriteConfigIfNeeded(g.registry.Config(), opts.GatewayBaseURL()); err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, op, "switchyard render")
	}
	return nil
}

const (
	// serverReadTimeout bounds reading one request, headers and body: a 26 MiB
	// audio upload on a slow link fits; a client that stalls mid-body does not.
	serverReadTimeout = 5 * time.Minute
	// serverIdleTimeout reaps idle keep-alive connections.
	serverIdleTimeout = 2 * time.Minute
	// shutdownTimeout bounds the graceful drain once ctx is done.
	shutdownTimeout = 15 * time.Second
)
