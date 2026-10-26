package gateway

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

func TestGatewayFromHandlerUnwrapsAccessProtector(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	reg, err := router.NewRegistry(gatedAdminBackendConfig("http://127.0.0.1:9/v1"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(config.ServeOptions{}, withRouter(&fakeRouter{reg: reg}), WithAdminStateDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := handler.(*gatewayHandler); ok {
		t.Fatal("expected wrapped handler from NewHandler")
	}
	gw, ok := gatewayFromHandler(handler)
	if !ok || gw == nil {
		t.Fatalf("gatewayFromHandler: ok=%v gw=%v type=%T", ok, gw, handler)
	}
}

func TestGatewayFromHandlerNil(t *testing.T) {
	gw, ok := gatewayFromHandler(nil)
	if ok || gw != nil {
		t.Fatalf("got gw=%v ok=%v", gw, ok)
	}
}

func TestGatewayFromHandlerBareGateway(t *testing.T) {
	gw := &gatewayHandler{}
	got, ok := gatewayFromHandler(http.Handler(gw))
	if !ok || got != gw {
		t.Fatalf("got %p want %p ok=%v", got, gw, ok)
	}
}

func TestServeStopsWhenTheContextIsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler(), ReadHeaderTimeout: time.Second})
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a cancelled serve shuts down cleanly: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after its context was cancelled")
	}
}

func TestListenAndServeRequiresAContext(t *testing.T) {
	//nolint:staticcheck // SA1012: a nil context is the case under test.
	if err := ListenAndServe(nil, config.ServeOptions{}); derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("a nil context must be refused: %v", err)
	}
}
