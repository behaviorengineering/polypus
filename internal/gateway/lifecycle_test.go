package gateway

import (
	"net/http"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

func TestGatewayFromHandlerUnwrapsAccessProtector(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	reg, err := router.NewRegistry(gatedAdminBackendConfig("http://127.0.0.1:9/v1"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(config.ServeOptions{}, WithRouter(&fakeRouter{reg: reg}), WithAdminStateDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := handler.(*Gateway); ok {
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
	gw := &Gateway{}
	got, ok := gatewayFromHandler(http.Handler(gw))
	if !ok || got != gw {
		t.Fatalf("got %p want %p ok=%v", got, gw, ok)
	}
}
