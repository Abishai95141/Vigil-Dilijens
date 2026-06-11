package qss

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

func TestRingAppendLatestLastN(t *testing.T) {
	h := NewHotStore()
	if _, ok := h.Latest("s"); ok {
		t.Error("empty store should have no latest")
	}
	for i := 0; i < 5; i++ {
		h.Append("s", Sample{At: t0.Add(time.Duration(i) * 15 * time.Second), Value: float64(i)})
	}
	latest, ok := h.Latest("s")
	if !ok || latest.Value != 4 {
		t.Errorf("latest = %+v, want value 4", latest)
	}
	last3 := h.LastN("s", 3)
	if len(last3) != 3 || last3[0].Value != 2 || last3[2].Value != 4 {
		t.Errorf("lastN = %+v, want [2 3 4] oldest-first", last3)
	}
	if got := h.LastN("s", 100); len(got) != 5 {
		t.Errorf("lastN over-ask = %d samples, want all 5", len(got))
	}
}

// The ring wraps at capacity: oldest samples shed first, order preserved.
func TestRingWrapAround(t *testing.T) {
	h := NewHotStore()
	n := hotCapacity + 17
	for i := 0; i < n; i++ {
		h.Append("s", Sample{At: t0.Add(time.Duration(i) * time.Second), Value: float64(i)})
	}
	all := h.LastN("s", hotCapacity+50)
	if len(all) != hotCapacity {
		t.Fatalf("population = %d, want capacity %d", len(all), hotCapacity)
	}
	if all[0].Value != float64(n-hotCapacity) || all[len(all)-1].Value != float64(n-1) {
		t.Errorf("window = [%v..%v], want [%d..%d]", all[0].Value, all[len(all)-1].Value, n-hotCapacity, n-1)
	}
	for i := 1; i < len(all); i++ {
		if all[i].Value != all[i-1].Value+1 {
			t.Fatalf("order broken at %d: %v after %v", i, all[i].Value, all[i-1].Value)
		}
	}
}

func TestStreamsAndKeys(t *testing.T) {
	h := NewHotStore()
	h.Append("b", Sample{At: t0, Value: 1})
	h.Append("a", Sample{At: t0, Value: 2})
	if h.Streams() != 2 {
		t.Errorf("streams = %d, want 2", h.Streams())
	}
	keys := h.Keys()
	if len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
		t.Errorf("keys = %v, want sorted [a b]", keys)
	}
}
