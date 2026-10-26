package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestSmokeOnlyProbesFromRun checks that nothing but `run` reaches the gateway:
// every probe is a paid upstream call.
func TestSmokeOnlyProbesFromRun(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("POLYPUS_BASE_URL", srv.URL)

	for _, tc := range []struct {
		args     []string
		wantCode int
		wantOut  string
	}{
		{nil, 0, "ROLE & BOUNDARIES"},
		{[]string{"version"}, 0, "polypus-smoke "},
		{[]string{"models"}, 0, "chat="},
		{[]string{"stray"}, 2, ""},
		{[]string{"run", "extra"}, 2, ""},
	} {
		code, out, errOut := captureWith(RunSmoke, tc.args)
		if code != tc.wantCode || !strings.Contains(out, tc.wantOut) {
			t.Errorf("polypus-smoke %v: exit %d out %q err %q", tc.args, code, out, errOut)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("only run may call the gateway, got %d requests", n)
	}

	code, out, _ := captureWith(RunSmoke, []string{"run", "--channels", "chat"})
	if code != 1 || hits.Load() == 0 || !strings.Contains(out, `"channel":"chat"`) {
		t.Fatalf("run must probe and exit 1 on failure: exit %d hits %d out %q", code, hits.Load(), out)
	}
}
