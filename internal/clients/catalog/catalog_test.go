package catalog

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func fixedClock() func() time.Time {
	now := time.Unix(1_000, 0)
	return func() time.Time { return now }
}

func TestCacheGetPutCopies(t *testing.T) {
	c := NewCacheClock(time.Minute, fixedClock())
	if _, ok := c.Get(); ok {
		t.Fatal("empty cache must miss")
	}
	c.Put([]Model{{ID: "a"}})
	got, ok := c.Get()
	if !ok || len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("got %#v", got)
	}
	got[0].ID = "changed"
	again, _ := c.Get()
	if again[0].ID != "a" {
		t.Fatal("Get must return a copy")
	}
}

func TestCacheExpires(t *testing.T) {
	now := time.Unix(1_000, 0)
	c := NewCacheClock(time.Minute, func() time.Time { return now })
	c.Put([]Model{{ID: "a"}})
	now = now.Add(2 * time.Minute)
	if _, ok := c.Get(); ok {
		t.Fatal("expired entries must miss")
	}
}

func TestListStrictFetchesThenServesCache(t *testing.T) {
	c := NewCacheClock(time.Minute, fixedClock())
	calls := 0
	fetch := func(context.Context) ([]Model, error) {
		calls++
		return []Model{{ID: "a"}}, nil
	}
	for range 2 {
		list, err := ListStrict(context.Background(), Source{Provider: "test", Cache: c, Fetch: fetch})
		if err != nil || list.Object != ObjectList || len(list.Data) != 1 {
			t.Fatalf("list=%#v err=%v", list, err)
		}
	}
	if calls != 1 {
		t.Fatalf("fetch calls: got %d want 1", calls)
	}
}

func TestListStrictReturnsErrorWithoutCache(t *testing.T) {
	boom := errors.New("boom")
	_, err := ListStrict(context.Background(), Source{Provider: "test", Cache: NewCacheClock(time.Minute, fixedClock()), Fetch: func(context.Context) ([]Model, error) {
		return nil, boom
	}})
	if !errors.Is(err, boom) {
		t.Fatalf("err: %v", err)
	}
}

func TestRegistryCreatesOnce(t *testing.T) {
	var r Registry[int]
	created := 0
	create := func() (*int, error) {
		created++
		v := created
		return &v, nil
	}
	a, _ := r.Get("k", "token-a", create)
	b, _ := r.Get("k", "token-a", create)
	if a != b || created != 1 {
		t.Fatalf("created=%d same=%v", created, a == b)
	}
	r.Reset()
	if c, _ := r.Get("k", "token-a", create); c == a {
		t.Fatal("Reset must drop clients")
	}
}

func TestRegistryReplacesClientOnTokenChange(t *testing.T) {
	var r Registry[string]
	build := func(token string) func() (*string, error) {
		return func() (*string, error) { return &token, nil }
	}
	old, err := r.Get("cf", "old-token\x00https://base", build("old-token"))
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := r.Get("cf", "new-token\x00https://base", build("new-token"))
	if err != nil {
		t.Fatal(err)
	}
	if rotated == old || *rotated != "new-token" {
		t.Fatalf("a rotated token must get a new client, got %q", *rotated)
	}
	if again, _ := r.Get("cf", "new-token\x00https://base", build("unused")); again != rotated {
		t.Fatal("the same fingerprint must reuse the client")
	}
	if moved, _ := r.Get("cf", "new-token\x00https://other", build("moved")); moved == rotated {
		t.Fatal("a changed base must get a new client")
	}
	if n := len(r.clients); n != 1 {
		t.Fatalf("one entry per backend after rotation, got %d", n)
	}
	for _, e := range r.clients {
		if e.client == old || *e.client == "old-token" {
			t.Fatal("the registry must not hold the client for the old token")
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("abcdef", 3); got != "abc..." {
		t.Fatalf("Truncate cut = %q", got)
	}
	if got := Truncate("  ab  ", 3); got != "ab" {
		t.Fatalf("Truncate short = %q", got)
	}
	if got := Truncate("héllo wörld", 2); got != "hé..." {
		t.Fatalf("Truncate must cut on runes, got %q", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := FirstNonEmpty("", "  ", " b ", "c"); got != "b" {
		t.Fatalf("FirstNonEmpty = %q, want trimmed b", got)
	}
	if got := FirstNonEmpty(" ", ""); got != "" {
		t.Fatalf("FirstNonEmpty blank = %q", got)
	}
}

func TestListStrictServesStaleCacheAfterTTLWhenFetchFails(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	c := NewCacheClock(time.Minute, func() time.Time { return now })
	c.Put([]Model{{ID: "a"}})
	now = now.Add(time.Hour) // well past the TTL.
	fetches := 0
	list, err := ListStrict(context.Background(), Source{Provider: "test", Cache: c, Fetch: func(context.Context) ([]Model, error) {
		fetches++
		return nil, errors.New("upstream down")
	}})
	if err != nil || fetches != 1 || len(list.Data) != 1 || list.Data[0].ID != "a" {
		t.Fatalf("want the stale list after a failed fetch: list=%+v err=%v fetches=%d", list, err, fetches)
	}
}

func TestListStrictWarnsStaleThroughTheSourceLogger(t *testing.T) {
	now := time.Unix(1_000, 0)
	c := NewCacheClock(time.Minute, func() time.Time { return now })
	c.Put([]Model{{ID: "a"}})
	now = now.Add(time.Hour)
	var buf bytes.Buffer
	_, err := ListStrict(context.Background(), Source{
		Provider: "gemini", Backend: "g1", Cache: c, Log: slog.New(slog.NewTextHandler(&buf, nil)),
		Fetch: func(context.Context) ([]Model, error) { return nil, errors.New("down") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "provider=gemini") || !strings.Contains(buf.String(), "backend=g1") {
		t.Fatalf("the warning names provider and backend as fields: %q", buf.String())
	}
}
