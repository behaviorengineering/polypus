package cloudflare

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/maximhq/bifrost/core/schemas"
)

func testClient(t *testing.T, srvURL string, now func() time.Time, sd SpeechDefaults) *Client {
	t.Helper()
	c, err := newClient(srvURL+"/client/v4/accounts/acct/ai", "token", now, sd)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSynthesizeMapsUpstream4xxToItsCode(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   derrors.Code
	}{
		{http.StatusUnauthorized, derrors.CodeUnauthorized},
		{http.StatusNotFound, derrors.CodeNotFound},
		{http.StatusBadRequest, derrors.CodeInvalid},
		{http.StatusBadGateway, derrors.CodeUnavailable},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"upstream says no"}]}`))
		}))
		c := testClient(t, srv.URL, time.Now, SpeechDefaults{})
		_, _, err := c.Synthesize(hopCtx(t), SpeechRequest{Input: "hi"})
		srv.Close()
		if got := derrors.CodeOf(err); got != tc.want {
			t.Fatalf("status %d: code %q, want %q (err %v)", tc.status, got, tc.want, err)
		}
	}
}

func TestSpeechKeepsTheOutboundCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("no call may leave without a deadline")
	}))
	defer srv.Close()
	c := testClient(t, srv.URL, time.Now, SpeechDefaults{})
	_, _, err := c.Synthesize(context.Background(), SpeechRequest{Input: "hi"})
	if got := derrors.CodeOf(err); got != derrors.CodeFailedPrecondition {
		t.Fatalf("missing deadline: code %q, want failed_precondition (err %v)", got, err)
	}
	_, err = c.SystemOne(context.Background(), "typesafe/jev", []byte(`{"state":"x"}`))
	if got := derrors.CodeOf(err); got != derrors.CodeFailedPrecondition {
		t.Fatalf("SystemOne missing deadline: code %q (err %v)", got, err)
	}
}

func TestTranscribeUsesInjectedSpeechDefaults(t *testing.T) {
	var gotPath, gotLang string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotLang = r.URL.Path, r.URL.Query().Get("language")
		_, _ = w.Write([]byte(`{"success":true,"result":{"text":"hello there"}}`))
	}))
	defer srv.Close()
	c := testClient(t, srv.URL, time.Now, SpeechDefaults{STTModel: "cf_local/@cf/openai/whisper", Language: "fr"})
	if _, err := c.Transcribe(hopCtx(t), TranscriptionRequest{Audio: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(gotPath, "/run/@cf/openai/whisper") || gotLang != "fr" {
		t.Fatalf("path %q language %q", gotPath, gotLang)
	}
	if d := (SpeechDefaults{}).withBuiltins(); d.TTSModel != builtinTTSModel || d.Voice != builtinVoice || d.Language != builtinLanguage {
		t.Fatalf("built-ins: %+v", d)
	}
}

func TestCatalogFailsPastThePageLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		_, _ = w.Write([]byte(`{"success":true,"result":[{"name":"@cf/m/` + strconv.Itoa(page) +
			`"}],"result_info":{"total_pages":1000}}`))
	}))
	defer srv.Close()
	c := testClient(t, srv.URL, time.Now, SpeechDefaults{})
	_, err := c.ListModelsStrict(hopCtx(t))
	if err == nil || !strings.Contains(err.Error(), "more than 50 pages") {
		t.Fatalf("want a page limit error, got %v", err)
	}
}

func TestCatalogCacheAgesOnTheInjectedClock(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"success":true,"result":[{"name":"@cf/m/a"}],"result_info":{"total_pages":1}}`))
	}))
	defer srv.Close()
	now := time.Unix(1_000, 0)
	c := testClient(t, srv.URL, func() time.Time { return now }, SpeechDefaults{})
	for range 2 {
		if _, err := c.ListModelsStrict(hopCtx(t)); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(modelsCacheTTL + time.Second)
	if _, err := c.ListModelsStrict(hopCtx(t)); err != nil {
		t.Fatal(err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("fetches = %d, want 2 (one cached read, one refetch past the TTL)", got)
	}
}

func TestClientsGetNeedsAClock(t *testing.T) {
	t.Setenv("CF_TEST_KEY", "secret")
	_, err := (&Clients{}).Get(config.BackendDef{
		ID: "cf", Extension: config.ExtensionCloudflare,
		BaseURL: "https://api.cloudflare.com/client/v4/accounts/a/ai/v1",
		Auth:    config.BackendAuth{BearerEnv: "CF_TEST_KEY"},
	})
	if derrors.CodeOf(err) != derrors.CodeInternal {
		t.Fatalf("want CodeInternal without a clock, got %v", err)
	}
}

func TestRunSpeechPluginRejectsNilContext(t *testing.T) {
	plugin := NewRunSpeechPlugin(func(string) (config.BackendDef, bool) {
		return config.BackendDef{}, false
	}, NewClient, slog.New(slog.DiscardHandler))
	req := &schemas.BifrostRequest{RequestType: schemas.SpeechRequest}
	_, sc, err := plugin.PreLLMHook(nil, req)
	if err != nil {
		t.Fatal(err)
	}
	if sc == nil || sc.Error == nil || derrors.CodeOf(sc.Error.Error.Error) != derrors.CodeInvalid {
		t.Fatalf("want an invalid short circuit, got %+v", sc)
	}
}
