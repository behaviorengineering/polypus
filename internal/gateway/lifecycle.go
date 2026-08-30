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
	g, ok := handler.(*Gateway)
	if !ok {
		return fmt.Errorf("gateway: unexpected handler type %T", handler)
	}
	defer g.Close()

	if err := writeSwitchyardConfig(g, opts); err != nil {
		return err
	}

	server := &http.Server{
		Addr:              opts.ListenAddr(),
		Handler:           observability.WrapHandler(handler),
		ReadHeaderTimeout: 30 * time.Second,
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
