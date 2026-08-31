package router

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/extension/cloudflare"
)

func TestHasCloudflareBackend(t *testing.T) {
	if hasCloudflareBackend(config.RouterConfig{
		Backends: map[string]config.BackendDef{
			"leaf": {ID: "leaf", BaseURL: "http://127.0.0.1:9"},
		},
	}) {
		t.Fatal("leaf-only must not report cloudflare")
	}
	if !hasCloudflareBackend(config.RouterConfig{
		Backends: map[string]config.BackendDef{
			"cf_local": {
				ID:        "cf_local",
				Remote:    true,
				Extension: config.ExtensionCloudflare,
				BaseURL:   "https://api.cloudflare.com/client/v4/accounts/x/ai/v1",
			},
		},
	}) {
		t.Fatal("cf extension must report cloudflare")
	}
}

func TestClientSynthesizeCFViaBifrostPlugin(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	t.Setenv("INFERENCE_CLOUD_CASE", "1")
	t.Setenv("CF_AI_API_KEY", "secret")

	runHits := 0
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/audio/") {
			t.Errorf("unexpected OpenAI audio path %s", r.URL.Path)
			http.Error(w, "no openai audio", http.StatusBadRequest)
			return
		}
		if !strings.Contains(r.URL.Path, "/run/") {
			http.NotFound(w, r)
			return
		}
		runHits++
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("via-plugin"))
	}))
	t.Cleanup(cf.Close)

	cfg := config.RouterConfig{
		Timeouts:            config.DefaultTimeouts(),
		DefaultTTSBackend:   "cf_local",
		DefaultSTTBackend:   "cf_local",
		DefaultChatBackend:  "cf_local",
		DefaultProxyBackend: "cf_local",
		Backends: map[string]config.BackendDef{
			"cf_local": {
				ID:        "cf_local",
				Remote:    true,
				Extension: config.ExtensionCloudflare,
				BaseURL:   cf.URL + "/client/v4/accounts/x/ai/v1",
				Auth:      config.BackendAuth{BearerEnv: "CF_AI_API_KEY"},
				Capabilities: []config.Capability{
					config.CapChat, config.CapTTS, config.CapSTT, config.CapVoices,
				},
				Models: &config.BackendModels{Allow: []string{"@cf/deepgram/aura-2-en"}},
			},
		},
	}
	client, err := NewClient(cfg, WithCloudflareClientGet(func(def config.BackendDef) (*cloudflare.Client, error) {
		return cloudflare.NewClient(def)
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)

	audio, err := client.Synthesize(context.Background(), SpeechRequest{
		Model:          "cf_local/@cf/deepgram/aura-2-en",
		Input:          "hi",
		Voice:          "luna",
		ResponseFormat: "mp3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(audio) != "via-plugin" {
		t.Fatalf("audio=%q", audio)
	}
	if runHits != 1 {
		t.Fatalf("runHits=%d", runHits)
	}
	if !client.cfSpeechPluginOn {
		t.Fatal("expected CF speech plugin registered")
	}
}

func TestClientTranscribeCFViaBifrostPlugin(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	t.Setenv("INFERENCE_CLOUD_CASE", "1")
	t.Setenv("CF_AI_API_KEY", "secret")

	runHits := 0
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/audio/") {
			t.Errorf("unexpected OpenAI audio path %s", r.URL.Path)
			http.Error(w, "no openai audio", http.StatusBadRequest)
			return
		}
		if !strings.Contains(r.URL.Path, "/run/") {
			http.NotFound(w, r)
			return
		}
		runHits++
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello from run"}`))
	}))
	t.Cleanup(cf.Close)

	cfg := config.RouterConfig{
		Timeouts:            config.DefaultTimeouts(),
		DefaultTTSBackend:   "cf_local",
		DefaultSTTBackend:   "cf_local",
		DefaultChatBackend:  "cf_local",
		DefaultProxyBackend: "cf_local",
		Backends: map[string]config.BackendDef{
			"cf_local": {
				ID:        "cf_local",
				Remote:    true,
				Extension: config.ExtensionCloudflare,
				BaseURL:   cf.URL + "/client/v4/accounts/x/ai/v1",
				Auth:      config.BackendAuth{BearerEnv: "CF_AI_API_KEY"},
				Capabilities: []config.Capability{
					config.CapChat, config.CapTTS, config.CapSTT, config.CapVoices,
				},
				Models: &config.BackendModels{Allow: []string{"@cf/deepgram/nova-3"}},
			},
		},
	}
	client, err := NewClient(cfg, WithCloudflareClientGet(func(def config.BackendDef) (*cloudflare.Client, error) {
		return cloudflare.NewClient(def)
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)

	body, ct, err := client.Transcribe(context.Background(), TranscriptionRequest{
		Model:          "cf_local/@cf/deepgram/nova-3",
		Audio:          []byte("fake-audio"),
		Filename:       "a.mp3",
		ResponseFormat: "json",
		Language:       "en",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "hello from run") {
		t.Fatalf("body=%q", body)
	}
	if !strings.Contains(ct, "json") {
		t.Fatalf("content-type=%q", ct)
	}
	if runHits != 1 {
		t.Fatalf("runHits=%d", runHits)
	}
}

func TestNewClientOmitsCFSpeechPluginWithoutCloudflare(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")

	leaf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/audio/speech" {
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("leaf-audio"))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(leaf.Close)

	cfg := config.RouterConfig{
		Timeouts:            config.DefaultTimeouts(),
		DefaultTTSBackend:   "leaf",
		DefaultSTTBackend:   "leaf",
		DefaultChatBackend:  "leaf",
		DefaultProxyBackend: "leaf",
		Backends: map[string]config.BackendDef{
			"leaf": {
				ID:           "leaf",
				BaseURL:      leaf.URL,
				Capabilities: []config.Capability{config.CapChat, config.CapTTS, config.CapSTT, config.CapVoices},
				Models:       &config.BackendModels{Allow: []string{"tts-1"}},
			},
		},
	}
	cfGetCalls := 0
	client, err := NewClient(cfg, WithCloudflareClientGet(func(def config.BackendDef) (*cloudflare.Client, error) {
		cfGetCalls++
		return cloudflare.NewClient(def)
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)

	if client.cfSpeechPluginOn {
		t.Fatal("leaf-only must not register CF speech plugin")
	}

	audio, err := client.Synthesize(context.Background(), SpeechRequest{
		Model:          "leaf/tts-1",
		Input:          "hi",
		ResponseFormat: "mp3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(audio) != "leaf-audio" {
		t.Fatalf("audio=%q", audio)
	}
	if cfGetCalls != 0 {
		t.Fatalf("cfGetCalls=%d", cfGetCalls)
	}
}
