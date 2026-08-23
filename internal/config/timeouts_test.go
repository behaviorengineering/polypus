package config

import (
	"testing"
	"time"
)

func TestParseTimeoutValue(t *testing.T) {
	d, err := ParseTimeoutValue("120s")
	if err != nil || d != 120*time.Second {
		t.Fatalf("120s: %v %v", d, err)
	}
	d, err = ParseTimeoutValue("2m")
	if err != nil || d != 2*time.Minute {
		t.Fatalf("2m: %v %v", d, err)
	}
	d, err = ParseTimeoutValue("90")
	if err != nil || d != 90*time.Second {
		t.Fatalf("90: %v %v", d, err)
	}
	if _, err := ParseTimeoutValue(""); err == nil {
		t.Fatal("expected empty error")
	}
	if _, err := ParseTimeoutValue("0"); err == nil {
		t.Fatal("expected non-positive error")
	}
}

func TestResolveChatBucketsAndHeader(t *testing.T) {
	to := DefaultTimeouts()
	if got := to.ResolveChat("", "lm_studio", false, false); got != 120*time.Second {
		t.Fatalf("lm chat: %s", got)
	}
	if got := to.ResolveChat("", "cf_local", false, false); got != 60*time.Second {
		t.Fatalf("cf chat: %s", got)
	}
	if got := to.ResolveChat("", "cf_local", false, true); got != 600*time.Second {
		t.Fatalf("thinking: %s", got)
	}
	if got := to.ResolveChat("", "lm_studio", true, false); got != 300*time.Second {
		t.Fatalf("vision: %s", got)
	}
	if got := to.ResolveChat("45s", "cf_local", false, false); got != 45*time.Second {
		t.Fatalf("header: %s", got)
	}
	if got := to.ResolveChat("2h", "cf_local", false, false); got != 900*time.Second {
		t.Fatalf("clamp max: %s", got)
	}
	if got := to.ResolveChat("1s", "cf_local", false, false); got != 5*time.Second {
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
	if got.ResolveChat("", "cf_local", false, false) != 30*time.Second {
		t.Fatalf("resolve after overlay: %s", got.ResolveChat("", "cf_local", false, false))
	}
}
