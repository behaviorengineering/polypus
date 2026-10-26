package otelcol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPhoenixSpanCountREST(t *testing.T) {
	const traceID = "0123456789abcdef0123456789abcdef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/v1/projects/default/spans") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("trace_id") != traceID {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": []interface{}{}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]string{{"name": "smoke"}},
		})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := phoenixSpanCountREST(ctx, CollectorOptions{
		PhoenixBaseURL: srv.URL,
		PhoenixProject: "default",
	}, traceID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 span, got %d", n)
	}
}

func TestPhoenixSpanCountGraphQLFallback(t *testing.T) {
	const traceID = "0123456789abcdef0123456789abcdef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/v1/projects/default/spans"):
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<!DOCTYPE html><html></html>"))
		case r.URL.Path == "/graphql" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"projects": map[string]interface{}{
						"edges": []map[string]interface{}{
							{"node": map[string]interface{}{
								"name": "default",
								"trace": map[string]interface{}{
									"spans": map[string]interface{}{
										"edges": []map[string]interface{}{
											{"node": map[string]string{"id": "span-1"}},
										},
									},
								},
							}},
						},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := phoenixSpanCount(ctx, CollectorOptions{
		PhoenixBaseURL: srv.URL,
		PhoenixProject: "default",
	}, traceID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 span, got %d", n)
	}
}

func TestHyperdxSearchV2(t *testing.T) {
	const traceID = "fedcba9876543210fedcba9876543210"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/sources":
			_ = json.NewEncoder(w).Encode([]hyperdxSource{{
				ID: "trace-src", Name: "Traces", Kind: "trace",
			}})
		case "/api/v2/search":
			_ = json.NewEncoder(w).Encode([]map[string]string{
				{"TraceId": traceID},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := hyperdxSearchV2(ctx, CollectorOptions{HyperDXBaseURL: srv.URL}, traceID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 row, got %d", n)
	}
}

func TestNormalizeTraceIDHex(t *testing.T) {
	id, err := normalizeTraceIDHex("0123456789ABCDEF0123456789abcdef")
	if err != nil || id != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("normalize: %v id=%q", err, id)
	}
	if _, err := normalizeTraceIDHex("short"); err == nil {
		t.Fatal("expected error for short id")
	}
	if _, err := normalizeTraceIDHex("0123456789abcdef0123456789abcdeg"); err == nil {
		t.Fatal("expected error for non-hex")
	}
}

func TestPickHyperDXTraceSource(t *testing.T) {
	id := pickHyperDXTraceSource([]hyperdxSource{
		{ID: "log", Name: "Logs", Kind: "log"},
		{ID: "tr", Name: "OTel Traces", Kind: "trace"},
	})
	if id != "tr" {
		t.Fatalf("expected trace source tr, got %q", id)
	}
}
