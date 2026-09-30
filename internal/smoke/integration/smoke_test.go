//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/smoke"
)

func TestMain(m *testing.M) {
	opts := Options{Live: LiveFromEnv(), Mode: ModeCloudflare}
	if err := StartShared(opts); err != nil {
		fmt.Fprintf(os.Stderr, "integration harness: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	if h, _ := Shared(); h != nil {
		h.Stop()
	}
	os.Exit(code)
}

func TestHarnessStartsAndHealth(t *testing.T) {
	h, err := Shared()
	if err != nil {
		t.Fatal(err)
	}
	if h.BaseURL == "" {
		t.Fatal("empty base URL")
	}
}

func TestSmokeChat(t *testing.T) {
	runChannel(t, smoke.ChannelChat)
}

func TestSmokeTTS(t *testing.T) {
	runChannel(t, smoke.ChannelTTS)
}

func TestSmokeSTT(t *testing.T) {
	runChannel(t, smoke.ChannelSTT)
}

func TestSmokeSystemOne(t *testing.T) {
	runChannel(t, smoke.ChannelSystemOne)
}

func TestSmokeBatch(t *testing.T) {
	if LiveFromEnv() && !BatchLiveFromEnv() {
		t.Skip("live batch smoke is opt-in (POLYPUS_SMOKE_BATCH=1); not part of default live CI")
	}
	runChannel(t, smoke.ChannelBatch)
}

func TestSmokeAll(t *testing.T) {
	h, err := Shared()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := smokeContext(h)
	defer cancel()
	results, err := h.RunSmoke(ctx, smoke.AllChannels())
	assertSmokeOK(t, results, err)
}

func runChannel(t *testing.T, channel string) {
	h, err := Shared()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := smokeContextForChannel(h, channel)
	defer cancel()
	results, err := h.RunSmoke(ctx, []string{channel})
	assertSmokeOK(t, results, err)
}

func smokeContext(h *Harness) (context.Context, context.CancelFunc) {
	timeout := 2 * time.Minute
	if h.Opts.Live {
		timeout = 10 * time.Minute
	}
	return context.WithTimeout(context.Background(), timeout)
}

func smokeContextForChannel(h *Harness, channel string) (context.Context, context.CancelFunc) {
	timeout := 2 * time.Minute
	if h.Opts.Live {
		timeout = 10 * time.Minute
		if channel == smoke.ChannelBatch {
			timeout = 25 * time.Minute
		}
	}
	return context.WithTimeout(context.Background(), timeout)
}

func TestSmokeTTSLocal(t *testing.T) {
	h := Start(t, Options{Mode: ModeMLX})
	runChannelOn(t, h, smoke.ChannelTTS, func(o *smoke.Options) {
		o.TTSModel = DefaultMLXTTSModel
		o.TTSVoice = DefaultMLXVoice
	})
}

func TestSmokeSTTLocal(t *testing.T) {
	h := Start(t, Options{Mode: ModeMLX})
	runChannelOn(t, h, smoke.ChannelSTT, func(o *smoke.Options) {
		o.TTSModel = DefaultMLXTTSModel
		o.STTModel = DefaultMLXSTTModel
		o.TTSVoice = DefaultMLXVoice
	})
}

func TestSmokeHiggs(t *testing.T) {
	h := Start(t, Options{Mode: ModeMLX})
	out := os.Getenv("POLYPUS_SMOKE_OUT")
	if strings.TrimSpace(out) == "" {
		out = t.TempDir() + "/polypus-higgs-smoke.mp3"
	}
	runChannelOn(t, h, smoke.ChannelTTS, func(o *smoke.Options) {
		o.TTSModel = DefaultHiggsTTSModel
		o.TTSVoice = DefaultMLXVoice
		o.AudioOutPath = out
	})
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("expected audio out file %q: %v", out, err)
	}
}

func TestSmokeRouter(t *testing.T) {
	h := Start(t, Options{Mode: ModeRouter})
	chatModel := strings.TrimSpace(os.Getenv("POLYPUS_ROUTER_SMOKE_MODEL"))
	if chatModel == "" {
		chatModel = DefaultRouterChatModel
	}
	runChannelOn(t, h, smoke.ChannelChat, func(o *smoke.Options) {
		o.ChatModel = chatModel
		o.RequireSelectedModel = true
		o.AllowedSelectedModels = []string{
			"cf_local/@cf/google/gemma-4-26b-a4b-it",
			"cf_local/@cf/zai-org/glm-4.7-flash",
		}
	})
}

func runChannelOn(t *testing.T, h *Harness, channel string, mutate func(*smoke.Options)) {
	ctx, cancel := smokeContextForChannel(h, channel)
	defer cancel()
	opts := h.SmokeOpts([]string{channel})
	if mutate != nil {
		mutate(&opts)
	}
	results, err := smoke.Run(ctx, opts)
	assertSmokeOK(t, results, err)
}

func assertSmokeOK(t *testing.T, results []smoke.Result, err error) {
	if err != nil {
		for _, r := range results {
			enc := json.NewEncoder(os.Stderr)
			_ = enc.Encode(r)
		}
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Status == "fail" {
			t.Fatalf("probe failed: %+v", r)
		}
	}
}
