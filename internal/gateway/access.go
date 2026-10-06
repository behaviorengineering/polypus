package gateway

import (
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/behaviorengineering/polypus/internal/admin/keys"
)

// accessProtector gates the gateway when the key store has at least one key.
type accessProtector struct {
	keys *keys.Store
	gw   *Gateway
}

func wrapGatewayAccess(store *keys.Store, gw *Gateway) http.Handler {
	if gw == nil {
		return nil
	}
	return &accessProtector{keys: store, gw: gw}
}

// gatewayFromHandler returns the inner Gateway when handler is wrapped for access control.
func gatewayFromHandler(handler http.Handler) (*Gateway, bool) {
	if handler == nil {
		return nil, false
	}
	if gw, ok := handler.(*Gateway); ok {
		return gw, true
	}
	if ap, ok := handler.(*accessProtector); ok && ap.gw != nil {
		return ap.gw, true
	}
	return nil, false
}

// Close releases owned router resources on the inner gateway.
func (a *accessProtector) Close() {
	if a != nil && a.gw != nil {
		a.gw.Close()
	}
}

func (a *accessProtector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a == nil || a.gw == nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if a.keys != nil {
		list, err := a.keys.List()
		if err != nil {
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
		if len(list) > 0 && !accessExempt(r) {
			token := extractAccessCredential(r)
			if token == "" {
				writeAccessUnauthorized(w)
				return
			}
			if _, err := a.keys.Verify(token); err != nil {
				writeAccessUnauthorized(w)
				return
			}
			r = r.Clone(r.Context())
			r.Header.Del("Authorization")
			r.Header.Del("X-Api-Key")
		}
	}
	a.gw.ServeHTTP(w, r)
}

func accessExempt(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	switch r.URL.Path {
	case "/health", "/health/backends", "/health/upstreams":
		return true
	default:
		return false
	}
}

func extractAccessCredential(r *http.Request) string {
	if r == nil {
		return ""
	}
	if k := strings.TrimSpace(r.Header.Get("X-Api-Key")); k != "" {
		return k
	}
	return bearerOrBasicToken(r.Header.Get("Authorization"))
}

func bearerOrBasicToken(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	if len(header) >= 7 && strings.EqualFold(header[:6], "bearer") {
		return strings.TrimSpace(header[6:])
	}
	if len(header) >= 6 && strings.EqualFold(header[:5], "basic") {
		raw := strings.TrimSpace(header[5:])
		dec, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return ""
		}
		user, pass, ok := strings.Cut(string(dec), ":")
		if !ok {
			return ""
		}
		pass = strings.TrimSpace(pass)
		if pass != "" {
			return pass
		}
		return strings.TrimSpace(user)
	}
	return ""
}

func writeAccessUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="polypus", Basic realm="polypus"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":{"message":"gateway access key required","type":"invalid_request_error","code":"unauthorized"}}`))
}
