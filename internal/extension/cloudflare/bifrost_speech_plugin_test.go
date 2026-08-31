package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/maximhq/bifrost/core/schemas"
)

func TestRunSpeechPluginShortCircuitsTTS(t *testing.T) {
	t.Setenv("INFERENCE_CLOUD_CASE", "1")
	t.Setenv("CF_AI_API_KEY", "secret")

	runHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/run/") {
			http.NotFound(w, r)
			return
		}
		runHits++
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		if body["text"] != "hello" || body["speaker"] != "luna" {
			t.Errorf("body=%v", body)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3fake"))
	}))
	t.Cleanup(srv.Close)

	b := config.BackendDef{
		ID:        "cf_local",
		Remote:    true,
		Extension: config.ExtensionCloudflare,
		BaseURL:   srv.URL + "/client/v4/accounts/x/ai/v1",
		Auth:      config.BackendAuth{BearerEnv: "CF_AI_API_KEY"},
		Capabilities: []config.Capability{
			config.CapChat, config.CapTTS, config.CapSTT,
		},
	}
	plugin := NewRunSpeechPlugin(func(provider string) (config.BackendDef, bool) {
		if provider == "cf_local" {
			return b, true
		}
		return config.BackendDef{}, false
	}, NewClient)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	bctx := schemas.NewBifrostContext(ctx, time.Now().Add(30*time.Second))
	req := &schemas.BifrostRequest{
		RequestType: schemas.SpeechRequest,
		SpeechRequest: &schemas.BifrostSpeechRequest{
			Provider: "cf_local",
			Model:    "@cf/deepgram/aura-2-en",
			Input:    &schemas.SpeechInput{Input: "hello"},
			Params: &schemas.SpeechParameters{
				ResponseFormat: "mp3",
				VoiceConfig:    &schemas.SpeechVoiceInput{Voice: schemas.Ptr("luna")},
			},
		},
	}
	_, sc, err := plugin.PreLLMHook(bctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if sc == nil || sc.Response == nil || sc.Response.SpeechResponse == nil {
		t.Fatalf("short-circuit=%v", sc)
	}
	if string(sc.Response.SpeechResponse.Audio) != "ID3fake" {
		t.Fatalf("audio=%q", sc.Response.SpeechResponse.Audio)
	}
	if runHits != 1 {
		t.Fatalf("runHits=%d", runHits)
	}
}

func TestRunSpeechPluginRejectsMissingDeadline(t *testing.T) {
	t.Setenv("INFERENCE_CLOUD_CASE", "1")
	t.Setenv("CF_AI_API_KEY", "secret")
	b := config.BackendDef{
		ID:           "cf_local",
		Remote:       true,
		Extension:    config.ExtensionCloudflare,
		BaseURL:      "https://api.cloudflare.com/client/v4/accounts/x/ai/v1",
		Auth:         config.BackendAuth{BearerEnv: "CF_AI_API_KEY"},
		Capabilities: []config.Capability{config.CapTTS},
	}
	plugin := NewRunSpeechPlugin(func(provider string) (config.BackendDef, bool) {
		if provider == "cf_local" {
			return b, true
		}
		return config.BackendDef{}, false
	}, NewClient)
	bctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	req := &schemas.BifrostRequest{
		RequestType: schemas.SpeechRequest,
		SpeechRequest: &schemas.BifrostSpeechRequest{
			Provider: "cf_local",
			Model:    "@cf/deepgram/aura-2-en",
			Input:    &schemas.SpeechInput{Input: "hello"},
			Params: &schemas.SpeechParameters{
				ResponseFormat: "mp3",
				VoiceConfig:    &schemas.SpeechVoiceInput{Voice: schemas.Ptr("luna")},
			},
		},
	}
	_, sc, err := plugin.PreLLMHook(bctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if sc == nil || sc.Error == nil || sc.Error.Error == nil {
		t.Fatalf("want deadline error, got %#v", sc)
	}
	if !strings.Contains(sc.Error.Error.Message, "deadline required") {
		t.Fatalf("msg=%q", sc.Error.Error.Message)
	}
	if !errors.Is(sc.Error.Error.Error, derrors.ErrFailedPrecondition) {
		t.Fatalf("want ErrFailedPrecondition, got %v", sc.Error.Error.Error)
	}
}

func TestRunSpeechPluginRejectsStream(t *testing.T) {
	t.Setenv("INFERENCE_CLOUD_CASE", "1")
	t.Setenv("CF_AI_API_KEY", "secret")
	b := config.BackendDef{
		ID:           "cf_local",
		Remote:       true,
		Extension:    config.ExtensionCloudflare,
		BaseURL:      "https://api.cloudflare.com/client/v4/accounts/x/ai/v1",
		Auth:         config.BackendAuth{BearerEnv: "CF_AI_API_KEY"},
		Capabilities: []config.Capability{config.CapTTS},
	}
	plugin := NewRunSpeechPlugin(func(provider string) (config.BackendDef, bool) {
		if provider == "cf_local" {
			return b, true
		}
		return config.BackendDef{}, false
	}, NewClient)
	bctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	req := &schemas.BifrostRequest{
		RequestType: schemas.SpeechStreamRequest,
		SpeechRequest: &schemas.BifrostSpeechRequest{
			Provider: "cf_local",
			Model:    "m",
			Input:    &schemas.SpeechInput{Input: "x"},
		},
	}
	_, sc, err := plugin.PreLLMHook(bctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if sc == nil || sc.Error == nil || sc.Error.Error == nil {
		t.Fatalf("want stream error, got %#v", sc)
	}
	if !strings.Contains(sc.Error.Error.Message, "streaming not supported") {
		t.Fatalf("msg=%q", sc.Error.Error.Message)
	}
}
