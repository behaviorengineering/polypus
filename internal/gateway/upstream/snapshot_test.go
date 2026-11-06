package upstream

import (
	"encoding/json"
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

func TestSnapshotReportsFailsafeMetrics(t *testing.T) {
	b := NewBoard()
	fail := errors.New("dial failed")
	_ = b.Execute("lm_studio", func() error { return nil })
	_ = b.Execute("lm_studio", func() error { return fail })
	snap := b.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("len=%d", len(snap))
	}
	got := snap[0]
	if got.Name != "lm_studio" || got.State != stateClosed {
		t.Fatalf("snapshot %+v", got)
	}
	if got.Executions != 2 || got.Successes != 1 || got.Failures != 1 {
		t.Fatalf("counts %+v", got)
	}
	if got.FailureRate <= 0 || got.FailureRate > 1 {
		t.Fatalf("failure_rate=%v", got.FailureRate)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	want := []string{"name", "state", "executions", "successes", "failures", "failure_rate"}
	if len(fields) != len(want) {
		t.Fatalf("/health/upstreams entry fields %v, want %v", fields, want)
	}
	for _, k := range want {
		if _, ok := fields[k]; !ok {
			t.Fatalf("missing %q in %s", k, raw)
		}
	}
}
