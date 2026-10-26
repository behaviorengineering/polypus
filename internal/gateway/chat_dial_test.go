package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProxyChatSendsAPaidPostOnce(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "7")
		http.Error(w, `{"error":{"message":"busy"}}`, http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_ = proxyChat(rec, r, []byte(`{"model":"m","messages":[]}`), chatDial{backendURL: srv.URL, hop: 5 * time.Second})
	if calls.Load() != 1 {
		t.Fatalf("a paid chat POST must be sent once, got %d calls", calls.Load())
	}
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "7" {
		t.Fatalf("the first 5xx must reach the caller with Retry-After: %d %v", rec.Code, rec.Header())
	}
}

func TestProxyChatStreamWithoutHeadersHitsTheHop(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	start := time.Now()
	err := proxyChat(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil),
		[]byte(`{"model":"m","stream":true,"messages":[]}`), chatDial{backendURL: srv.URL, hop: 100 * time.Millisecond})
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("a stream with no headers must fail at the hop: err=%v after %s", err, time.Since(start))
	}
}

func TestProxyChatStreamThatStallsIsCutOff(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		select { // Then go quiet.
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	rec := httptest.NewRecorder()
	start := time.Now()
	err := proxyChat(rec, httptest.NewRequest(http.MethodPost, "/", nil),
		[]byte(`{"model":"m","stream":true,"messages":[]}`),
		chatDial{backendURL: srv.URL, hop: 5 * time.Second, streamIdle: 150 * time.Millisecond})
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("a stalled stream must be cut off at the idle limit: err=%v after %s", err, time.Since(start))
	}
	if !strings.Contains(rec.Body.String(), "hi") {
		t.Fatalf("the chunks before the stall must reach the client: %q", rec.Body.String())
	}
}

func TestOversizedUpstreamReplyIsAnErrorNotATruncated200(t *testing.T) {
	if _, err := readUpstream(strings.NewReader(strings.Repeat("x", 64)), 63, "test"); err == nil {
		t.Fatal("a body past the cap must fail")
	}
	if raw, err := readUpstream(strings.NewReader(strings.Repeat("x", 64)), 64, "test"); err != nil || len(raw) != 64 {
		t.Fatalf("a body at the cap must pass: %d %v", len(raw), err)
	}
}

func TestOversizedChatRequestGets413(t *testing.T) {
	h, _ := newOpenRouterBatchGateway(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(strings.Repeat("a", chatMaxBody+1)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413", rec.Code)
	}
}
