package gateway

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// adminAllowJSONPost is a curl --json shaped POST to the allow route.
func adminAllowJSONPost(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, pathAdminModelsAllow, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

type adminAllowFixture struct {
	handler http.Handler
	reg     *router.Registry
	dir     string
}

// newAdminAllowFixture serves a gated backend "cf" (allow: @cf/a) whose inventory
// also lists @cf/b, the model the tests try to add.
func newAdminAllowFixture(t *testing.T) adminAllowFixture {
	t.Helper()
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	modelsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []map[string]any{{"id": "@cf/a", "object": "model"}, {"id": "@cf/b", "object": "model"}},
		})
	}))
	t.Cleanup(modelsSrv.Close)
	reg, err := router.NewRegistry(gatedAdminBackendConfig(modelsSrv.URL + "/v1"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	h := newTestGateway(t, config.ServeOptions{}, &fakeRouter{reg: reg}, WithAdminStateDir(dir))
	return adminAllowFixture{handler: h, reg: reg, dir: dir}
}

func (f adminAllowFixture) assertOverlayUnchanged(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(f.dir, "models-allow-overlay.yaml")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("overlay file must not be written: %v", err)
	}
	b, _ := f.reg.Backend("cf")
	if b.IsModelAllowed("@cf/b") {
		t.Fatal("a refused request must not change the in-memory allow list")
	}
}

func (f adminAllowFixture) assertAllowed(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	b, _ := f.reg.Backend("cf")
	if !b.IsModelAllowed("@cf/b") {
		t.Fatal("expected @cf/b in the allow list")
	}
}

const adminAllowBody = `{"backend":"cf","model":"@cf/b"}`

func TestAdminAllowRefusesNonJSON(t *testing.T) {
	for _, ct := range []string{"text/plain", "text/plain;charset=UTF-8", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", ""} {
		t.Run(ct, func(t *testing.T) {
			f := newAdminAllowFixture(t)
			req := httptest.NewRequest(http.MethodPost, pathAdminModelsAllow, strings.NewReader(adminAllowBody))
			if ct != "" {
				req.Header.Set("Content-Type", ct)
			}
			rec := httptest.NewRecorder()
			f.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "--json") {
				t.Fatalf("the refusal should say how to fix it: %s", rec.Body.String())
			}
			f.assertOverlayUnchanged(t)
		})
	}
}

func TestAdminAllowRefusesCrossSiteOrigin(t *testing.T) {
	for _, origin := range []string{"https://evil.example", "null", "http://127.0.0.1:9999"} {
		t.Run(origin, func(t *testing.T) {
			f := newAdminAllowFixture(t)
			req := adminAllowJSONPost(adminAllowBody)
			req.Host = "127.0.0.1:1320"
			req.Header.Set("Origin", origin)
			rec := httptest.NewRecorder()
			f.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
			f.assertOverlayUnchanged(t)
		})
	}
}

func TestAdminAllowAcceptsNoOriginJSON(t *testing.T) {
	f := newAdminAllowFixture(t)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, adminAllowJSONPost(adminAllowBody))
	f.assertAllowed(t, rec)
}

func TestAdminAllowAcceptsSameOriginLanding(t *testing.T) {
	f := newAdminAllowFixture(t)
	req := adminAllowJSONPost(adminAllowBody)
	req.Host = "127.0.0.1:1320"
	req.Header.Set("Origin", "http://127.0.0.1:1320")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	f.assertAllowed(t, rec)
}

func TestAdminAllowAcceptsSameOriginBehindProxy(t *testing.T) {
	f := newAdminAllowFixture(t)
	req := adminAllowJSONPost(adminAllowBody)
	req.Host = "polypus-gateway:1320" // The compose service name the proxy dials.
	req.Header.Set("Origin", "https://polypus.tail1234.ts.net")
	req.Header.Set("X-Forwarded-Host", "polypus.tail1234.ts.net")
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	f.assertAllowed(t, rec)
}

func TestSameOriginComparesSchemeHostAndPort(t *testing.T) {
	tests := []struct {
		name, origin, host, fwdHost, fwdProto string
		want                                  bool
	}{
		{name: "loopback match", origin: "http://127.0.0.1:1320", host: "127.0.0.1:1320", want: true},
		{name: "other port", origin: "http://127.0.0.1:1321", host: "127.0.0.1:1320"},
		{name: "scheme differs", origin: "https://127.0.0.1:1320", host: "127.0.0.1:1320"},
		{name: "default https port", origin: "https://gw.ts.net", host: "x", fwdHost: "gw.ts.net:443", fwdProto: "https", want: true},
		{name: "first forwarded value", origin: "https://gw.ts.net", host: "x", fwdHost: "gw.ts.net, inner", fwdProto: "https, http", want: true},
		{name: "case-insensitive host", origin: "http://LOCALHOST:1320", host: "localhost:1320", want: true},
		{name: "opaque null", origin: "null", host: "127.0.0.1:1320"},
		{name: "ipv6 loopback", origin: "http://[::1]:1320", host: "[::1]:1320", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, pathAdminModelsAllow, nil)
			r.Host = tc.host
			if tc.fwdHost != "" {
				r.Header.Set("X-Forwarded-Host", tc.fwdHost)
			}
			if tc.fwdProto != "" {
				r.Header.Set("X-Forwarded-Proto", tc.fwdProto)
			}
			if got := sameOrigin(tc.origin, r); got != tc.want {
				t.Fatalf("sameOrigin(%q) = %v, want %v", tc.origin, got, tc.want)
			}
		})
	}
}

func TestSameOriginReadsTheSchemeLikeTheLandingPage(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, pathAdminModelsAllow, nil)
	r.Host = "gw.example"
	r.Header.Set("X-Forwarded-Proto", "ftp")
	if !sameOrigin("http://gw.example", r) {
		t.Fatal("a forwarded scheme other than http or https must be ignored, as requestScheme does")
	}
	r.Header.Set("X-Forwarded-Proto", "HTTPS, http")
	if !sameOrigin("https://gw.example", r) || sameOrigin("http://gw.example", r) {
		t.Fatal("the first forwarded value names the scheme")
	}
}
