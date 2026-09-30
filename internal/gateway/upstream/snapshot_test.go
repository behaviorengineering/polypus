package upstream

import (
	"errors"
	"testing"
)

func TestBoardSnapshotAfterTrip(t *testing.T) {
	b := NewBoard()
	fail := errors.New("dial failed")
	for i := 0; i < 3; i++ {
		_ = b.Execute("cf_local", func() error { return fail })
	}
	snap := b.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("len=%d", len(snap))
	}
	if snap[0].Name != "cf_local" {
		t.Fatalf("name=%s", snap[0].Name)
	}
	if snap[0].State != "open" {
		t.Fatalf("state=%s", snap[0].State)
	}
}
