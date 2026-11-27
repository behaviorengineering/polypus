package gateway

import (
	"net/http"

	"github.com/behaviorengineering/polypus/internal/gateway/landing"
)

func (g *gatewayHandler) serveLanding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		g.writeMethodNotAllowed(w, allowRead...)
		return
	}
	landing.WritePage(w, r, g.registry.Config(), g.log)
}

func (g *gatewayHandler) serveBannerWebP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		g.writeMethodNotAllowed(w, allowRead...)
		return
	}
	landing.WriteBanner(w, r, g.log)
}
