package catalog

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCacheGetPutCopies(t *testing.T) {
	c := NewCache(time.Minute)
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
	c := NewCache(time.Nanosecond)
	c.Put([]Model{{ID: "a"}})
	time.Sleep(time.Millisecond)
	if _, ok := c.Get(); ok {
		t.Fatal("expired entries must miss")
	}
}

func TestListStrictFetchesThenServesCache(t *testing.T) {
	c := NewCache(time.Minute)
	calls := 0
	fetch := func(context.Context) ([]Model, error) {
		calls++
		return []Model{{ID: "a"}}, nil
	}
	for range 2 {
		list, err := ListStrict(context.Background(), "test", c, fetch)
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
	_, err := ListStrict(context.Background(), "test", NewCache(time.Minute), func(context.Context) ([]Model, error) {
		return nil, boom
	})
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
	a, _ := r.Get("k", create)
	b, _ := r.Get("k", create)
	if a != b || created != 1 {
		t.Fatalf("created=%d same=%v", created, a == b)
	}
	r.Reset()
	if c, _ := r.Get("k", create); c == a {
		t.Fatal("Reset must drop clients")
	}
}

func TestTruncate(t *testing.T) {
	if Truncate("abcdef", 3) != "abc" || Truncate("ab", 3) != "ab" {
		t.Fatal("truncate")
	}
}

func TestListStrictServesStaleCacheAfterTTLWhenFetchFails(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	c := NewCacheClock(time.Minute, func() time.Time { return now })
	c.Put([]Model{{ID: "a"}})
	now = now.Add(time.Hour) // well past the TTL.
	fetches := 0
	list, err := ListStrict(context.Background(), "test", c, func(context.Context) ([]Model, error) {
		fetches++
		return nil, errors.New("upstream down")
	})
	if err != nil || fetches != 1 || len(list.Data) != 1 || list.Data[0].ID != "a" {
		t.Fatalf("want the stale list after a failed fetch: list=%+v err=%v fetches=%d", list, err, fetches)
	}
}
