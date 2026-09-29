package batch

import (
	"testing"
	"time"
)

func TestParseCompletionWindow(t *testing.T) {
	d, err := ParseCompletionWindow("24h")
	if err != nil || d != 24*time.Hour {
		t.Fatalf("24h: %v %v", d, err)
	}
	d, err = ParseCompletionWindow("90m")
	if err != nil || d != 90*time.Minute {
		t.Fatalf("90m: %v %v", d, err)
	}
	if _, err := ParseCompletionWindow("bad"); err == nil {
		t.Fatal("expected error")
	}
}

func TestExpiresAtUnix(t *testing.T) {
	at, err := ExpiresAtUnix(1000, "24h")
	if err != nil || at != 1000+24*3600 {
		t.Fatalf("expires: %d %v", at, err)
	}
}
