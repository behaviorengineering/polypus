//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/smoke"
)

func TestMain(m *testing.M) {
	opts := Options{Live: LiveFromEnv()}
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
	ctx, cancel := smokeContext(h)
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
