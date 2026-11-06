package router

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/ratelimit"
	"github.com/maximhq/bifrost/core/schemas"
)

// testLogger is the logger NewClient requires, discarding everything.
func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestNewClientRequiresLogger(t *testing.T) {
	_, err := NewClient(config.RouterConfig{Timeouts: config.DefaultTimeouts()})
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("a missing logger must be CodeInvalid, got %v", err)
	}
}

func badBackendConfig() config.RouterConfig {
	return config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"bad_leaf": {ID: "bad_leaf", BaseURL: "ftp://127.0.0.1:1", Capabilities: []config.Capability{config.CapChat}},
		},
	}
}

func TestNewRegistryTypesABadBackend(t *testing.T) {
	_, err := NewRegistry(badBackendConfig())
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("a bad base_url is the operator's input, got %v", err)
	}
	if derrors.Field(err, fieldBackend) != "bad_leaf" {
		t.Fatalf("the error must name the backend: %v", err)
	}
}

func TestNewClientKeepsTheRegistryCode(t *testing.T) {
	_, err := NewClient(badBackendConfig(), WithLogger(testLogger()))
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("a bad backend must not read as a Polypus bug (CodeInternal), got %v", err)
	}
}

func TestSpeechTargetKeepsResolveCodes(t *testing.T) {
	reg := testRegistry(t)
	c := &Client{reg: reg}
	provider, model, err := c.speechTarget("router.Synthesize", "tts", reg.ResolveTTS, "default-voice", "")
	if err != nil || provider != "mlx_local" || model != "default-voice" {
		t.Fatalf("default route: %q %q %v", provider, model, err)
	}
	if _, _, err := c.speechTarget("router.Synthesize", "tts", reg.ResolveTTS, "", "mlx_local/"); derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("a bad request must stay CodeInvalid (400), got %v", err)
	}
	if _, _, err := c.speechTarget("router.Synthesize", "tts", reg.ResolveTTS, "", "alt_stt/whisper"); derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("a backend without the capability must stay CodeInvalid (400), got %v", err)
	}

	cfg := reg.Config()
	cfg.TTS.Default = "gone"
	noDefault := &Registry{}
	noDefault.storeCfg(cfg)
	c = &Client{reg: noDefault}
	if _, _, err := c.speechTarget("router.Synthesize", "tts", noDefault.ResolveTTS, "", ""); derrors.CodeOf(err) != derrors.CodeNotReady {
		t.Fatalf("a missing default backend must stay CodeNotReady (503), got %v", err)
	}
}

func TestAnthropicChatErrKeepsProviderCause(t *testing.T) {
	cause := derrors.New(derrors.CodeInvalid, "provider", "bad tool schema").With("param", "tools.0")
	berr := bifrostTestErr(400, "invalid_request_error", "tools.0: invalid schema")
	berr.Error.Error = cause
	err := anthropicChatErr(berr)
	if derrors.HTTPStatus(err) != http.StatusBadRequest {
		t.Fatalf("status: %v", err)
	}
	if !errors.Is(err, cause) || derrors.Field(err, "param") != "tools.0" {
		t.Fatalf("the provider cause must stay in the chain: %+v", err)
	}
	if derrors.Field(err, ratelimit.FieldStatus) != "400" {
		t.Fatalf("status field: %+v", err)
	}
}

func TestBifrostErrRateLimitKeepsProviderCause(t *testing.T) {
	cause := errors.New("provider says slow down")
	status := http.StatusTooManyRequests
	err := bifrostErr(&schemas.BifrostError{
		StatusCode: &status,
		Error:      &schemas.ErrorField{Message: "slow down", Error: cause},
	}, nil, false)
	if derrors.CodeOf(err) != derrors.CodeRateLimited {
		t.Fatalf("code: %v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("the provider cause must stay in the chain: %v", err)
	}
	if derrors.Field(err, ratelimit.FieldStatus) != "429" {
		t.Fatalf("the rate limit's fields must win: %+v", err)
	}
}

// TestMapBifrostErrUsesTheKnownBackend shows why the speech paths pass the resolved
// backend id: the Cloudflare speech plugin's error names no provider, so without the
// id a Cloudflare throttle code is not read.
func TestMapBifrostErrUsesTheKnownBackend(t *testing.T) {
	t.Setenv("CF_AI_API_KEY", "secret")
	reg, err := NewRegistry(config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"cf_local": {
				ID:           "cf_local",
				Remote:       true,
				Extension:    config.ExtensionCloudflare,
				BaseURL:      "https://api.cloudflare.com/client/v4/accounts/x/ai/v1",
				Auth:         config.BackendAuth{BearerEnv: "CF_AI_API_KEY"},
				Capabilities: []config.Capability{config.CapTTS},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{reg: reg}
	pluginErr := func() *schemas.BifrostError {
		inner := derrors.New(derrors.CodeUnavailable, "cloudflare.speech", "error 3040: capacity exceeded")
		return &schemas.BifrostError{Error: &schemas.ErrorField{Message: inner.Error(), Error: inner}}
	}
	if got := c.mapBifrostErr("", pluginErr(), nil); derrors.CodeOf(got) == derrors.CodeRateLimited {
		t.Fatalf("without a backend id the throttle is not Cloudflare's: %v", got)
	}
	got := c.mapBifrostErr("cf_local", pluginErr(), nil)
	if derrors.CodeOf(got) != derrors.CodeRateLimited || derrors.Field(got, ratelimit.FieldKind) != ratelimit.KindCapacity {
		t.Fatalf("the backend id must select Cloudflare classification: %+v", got)
	}
}

func TestBifrostContextCancelReleasesIt(t *testing.T) {
	parent, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	c := &Client{}
	bctx, cancel := c.bifrostContext(parent, time.Minute, true)
	if bctx.Value(schemas.BifrostContextKeyUseRawRequestBody) != true {
		t.Fatal("raw body flag not set")
	}
	cancel()
	select {
	case <-bctx.Done():
	case <-time.After(time.Second):
		t.Fatal("cancel must end the Bifrost context (and its watcher goroutine)")
	}
	if !errors.Is(bctx.Err(), context.Canceled) {
		t.Fatalf("err=%v", bctx.Err())
	}
	if parent.Err() != nil {
		t.Fatal("cancel must not end the caller's context")
	}
	if bctx.Value(schemas.BifrostContextKeyUseRawRequestBody) != true {
		t.Fatal("values must stay readable after cancel")
	}
}

func TestAccountErrorsAreTyped(t *testing.T) {
	t.Setenv("MISSING_TEST_KEY", "")
	acct := newAccount(config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"remote_leaf": {
				ID:           "remote_leaf",
				Remote:       true,
				BaseURL:      "https://llm.example.com/v1",
				Auth:         config.BackendAuth{BearerEnv: "MISSING_TEST_KEY"},
				Capabilities: []config.Capability{config.CapChat},
			},
		},
	})
	_, err := acct.GetKeysForProvider(context.Background(), "remote_leaf")
	if err == nil {
		t.Fatal("expected a credential error")
	}
	if code := derrors.CodeOf(err); code == derrors.CodeUnavailable || code == derrors.CodeInternal {
		t.Fatalf("a missing credential must not read as a provider failure, got %v", err)
	}
	if _, err := acct.GetKeysForProvider(context.Background(), "nope"); derrors.CodeOf(err) != derrors.CodeNotFound || derrors.Field(err, fieldProvider) != "nope" {
		t.Fatalf("unknown provider: %v", err)
	}
	if _, err := acct.GetConfigForProvider("nope"); derrors.CodeOf(err) != derrors.CodeNotFound {
		t.Fatalf("unknown provider: %v", err)
	}
}
