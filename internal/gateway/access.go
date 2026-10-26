package gateway

import (
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/behaviorengineering/polypus/internal/admin/keys"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// accessProtector gates the gateway when the key store has at least one key.
type accessProtector struct {
	keys *keys.Store
	gw   *gatewayHandler
}

func wrapGatewayAccess(store *keys.Store, gw *gatewayHandler) http.Handler {
	if gw == nil {
		panic("gateway: wrapGatewayAccess(nil gateway)")
	}
	return &accessProtector{keys: store, gw: gw}
}

// gatewayFromHandler returns the inner Gateway when handler is wrapped for access control.
func gatewayFromHandler(handler http.Handler) (*gatewayHandler, bool) {
	if handler == nil {
		return nil, false
	}
	if gw, ok := handler.(*gatewayHandler); ok {
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
		// One read of the key file decides both whether the gateway is locked and
		// whether this request's key opens it. A store that cannot be read fails
		// closed, even for the liveness probe.
		open, err := a.keys.Authorize(extractAccessCredential(r))
		unauthorized := derrors.CodeOf(err) == derrors.CodeUnauthorized
		switch {
		case err != nil && !unauthorized:
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		case accessExempt(r):
			// The liveness probe needs no key.
		case unauthorized:
			a.writeAccessUnauthorized(w)
			return
		case !open:
			r = r.Clone(r.Context())
			r.Header.Del("Authorization")
			r.Header.Del("X-Api-Key")
		}
	}
	a.gw.ServeHTTP(w, r)
}

// accessExempt lets only the liveness probe through without a key. /health/backends
// and /health/upstreams name backend URLs and dial upstreams, so they need the key.
func accessExempt(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return r.URL.Path == "/health"
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
	// The scheme needs its separating space: "BearerXYZ" is not a bearer token.
	if len(header) > 7 && strings.EqualFold(header[:7], "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	if len(header) > 6 && strings.EqualFold(header[:6], "basic ") {
		raw := strings.TrimSpace(header[6:])
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

func (a *accessProtector) writeAccessUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="polypus", Basic realm="polypus"`)
	a.gw.writeOpenAIError(w, http.StatusUnauthorized, openaiError{
		Message: "gateway access key required", Type: errTypeInvalidRequest, Code: "unauthorized",
	})
}
