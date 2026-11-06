package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestKeylessReadsTheAdminRouteWithoutAKey(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		want   bool
	}{
		"open gateway":   {http.StatusOK, true},
		"locked gateway": {http.StatusUnauthorized, false},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != keylessProbePath || r.Header.Get("Authorization") != "" {
					t.Errorf("unexpected probe %s with auth %q", r.URL.Path, r.Header.Get("Authorization"))
				}
				w.WriteHeader(tc.status)
			}))
			t.Cleanup(srv.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			got, err := Keyless(ctx, srv.Client(), srv.URL)
			if err != nil || got != tc.want {
				t.Fatalf("Keyless = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}
