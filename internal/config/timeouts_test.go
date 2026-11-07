package config

import (
	"context"
	"testing"
	"time"
)

func TestParseTimeoutValue(t *testing.T) {
	d, err := parseTimeoutValue("120s")
	if err != nil || d != 120*time.Second {
		t.Fatalf("120s: %v %v", d, err)
	}
	d, err = parseTimeoutValue("2m")
	if err != nil || d != 2*time.Minute {
		t.Fatalf("2m: %v %v", d, err)
	}
	d, err = parseTimeoutValue("90")
	if err != nil || d != 90*time.Second {
		t.Fatalf("90: %v %v", d, err)
	}
	if _, err := parseTimeoutValue(""); err == nil {
		t.Fatal("expected empty error")
	}
	if _, err := parseTimeoutValue("0"); err == nil {
		t.Fatal("expected non-positive error")
	}
}

func TestResolveChatBucketsAndHeader(t *testing.T) {
	to := DefaultTimeouts()
	to.Backends["cf_local"] = BackendTimeouts{Chat: defaultTimeoutChatCF}
	if got := to.ResolveChat("", "lm_studio", ChatKindOf(false, false)); got != 120*time.Second {
		t.Fatalf("lm chat: %s", got)
	}
	if got := to.ResolveChat("", "cf_local", ChatKindOf(false, false)); got != 60*time.Second {
		t.Fatalf("cf chat: %s", got)
	}
	if got := to.ResolveChat("", "cf_local", ChatKindOf(false, true)); got != 600*time.Second {
		t.Fatalf("thinking: %s", got)
	}
	if got := to.ResolveChat("", "lm_studio", ChatKindOf(true, false)); got != 300*time.Second {
		t.Fatalf("vision: %s", got)
	}
	if got := to.ResolveChat("45s", "cf_local", ChatKindOf(false, false)); got != 45*time.Second {
		t.Fatalf("header: %s", got)
	}
	if got := to.ResolveChat("2h", "cf_local", ChatKindOf(false, false)); got != 900*time.Second {
		t.Fatalf("clamp max: %s", got)
	}
	if got := to.ResolveChat("1s", "cf_local", ChatKindOf(false, false)); got != 5*time.Second {
		t.Fatalf("clamp min: %s", got)
	}
}

func TestParseTimeoutsFileOverlay(t *testing.T) {
	got, err := parseTimeoutsFile(timeoutsFile{
		Chat: "90s",
		Backends: map[string]backendTOFile{
			"cf_local": {Chat: "30s"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Chat != 90*time.Second {
		t.Fatalf("chat overlay: %s", got.Chat)
	}
	if got.Backends["cf_local"].Chat != 30*time.Second {
		t.Fatalf("cf overlay: %s", got.Backends["cf_local"].Chat)
	}
	if got.ResolveChat("", "cf_local", ChatKindOf(false, false)) != 30*time.Second {
		t.Fatalf("resolve after overlay: %s", got.ResolveChat("", "cf_local", ChatKindOf(false, false)))
	}
}

func TestResolveEmbedDefault(t *testing.T) {
	to := DefaultTimeouts()
	if got := to.ResolveEmbed(""); got != 15*time.Second {
		t.Fatalf("default embed: %s", got)
	}
	if got := to.ResolveEmbed("45s"); got != 45*time.Second {
		t.Fatalf("header: %s", got)
	}
	if got := to.ResolveEmbed("1s"); got != 5*time.Second {
		t.Fatalf("clamp min: %s", got)
	}
	omitted, err := parseTimeoutsFile(timeoutsFile{Chat: "90s"})
	if err != nil {
		t.Fatal(err)
	}
	if omitted.ResolveEmbed("") != 15*time.Second {
		t.Fatalf("omitted yaml embed: %s", omitted.ResolveEmbed(""))
	}
}

func TestResolveSpeechBucketsAndHeader(t *testing.T) {
	to := DefaultTimeouts()
	if got := to.ResolveSpeech(""); got != 180*time.Second {
		t.Fatalf("default speech: %s", got)
	}
	if got := to.ResolveSpeech("45s"); got != 45*time.Second {
		t.Fatalf("header: %s", got)
	}
	if got := to.ResolveSpeech("1s"); got != 5*time.Second {
		t.Fatalf("clamp min: %s", got)
	}
}

// TestCloudflareChatTimeoutFollowsTheExtension keys the Cloudflare chat budget on
// the backend's extension, not on the id cf_local.
func TestCloudflareChatTimeoutFollowsTheExtension(t *testing.T) {
	cfg := RouterConfig{
		Timeouts: DefaultTimeouts(),
		Backends: map[string]BackendDef{
			"workers":  {ID: "workers", Extension: ExtensionCloudflare},
			"cf_local": {ID: "cf_local"},
			"cf_slow":  {ID: "cf_slow", Extension: ExtensionCloudflare},
		},
	}
	cfg.Timeouts.Backends["cf_slow"] = BackendTimeouts{Chat: 200 * time.Second}
	fillCloudflareChatTimeouts(&cfg)
	plain := ChatKindOf(false, false)
	for id, want := range map[string]time.Duration{
		"workers":  defaultTimeoutChatCF,
		"cf_local": defaultTimeoutChat,
		"cf_slow":  200 * time.Second,
	} {
		if got := cfg.Timeouts.ResolveChat("", id, plain); got != want {
			t.Errorf("%s chat timeout %s, want %s", id, got, want)
		}
	}
}

func TestCallerBudgetMarker(t *testing.T) {
	ctx := context.Background()
	if CallerBudgetFrom(ctx) {
		t.Fatal("an unmarked context has no caller budget")
	}
	if !CallerBudgetFrom(WithCallerBudget(ctx)) {
		t.Fatal("WithCallerBudget must mark the context")
	}
	child, cancel := context.WithTimeout(WithCallerBudget(ctx), time.Second)
	defer cancel()
	if !CallerBudgetFrom(child) {
		t.Fatal("the mark must survive a derived context")
	}
	var nilCtx context.Context // Nil on purpose.
	if CallerBudgetFrom(nilCtx) {
		t.Fatal("a nil context is not marked")
	}
}
