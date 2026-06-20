package candidate

import (
	"testing"
	"time"
)

func mustOpenGap(t *testing.T) *Store {
	t.Helper()
	st, err := Open("")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestBackoffInterval(t *testing.T) {
	base := time.Minute
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{-1, base}, {0, base}, {1, 2 * base}, {2, 4 * base}, {3, 8 * base},
		{GapBackoffCap, base * time.Duration(int64(1)<<GapBackoffCap)},
		{GapBackoffCap + 5, base * time.Duration(int64(1)<<GapBackoffCap)}, // capped
	}
	for _, c := range cases {
		if got := backoffInterval(base, c.attempts, GapBackoffCap); got != c.want {
			t.Errorf("backoffInterval(attempts=%d) = %s, want %s", c.attempts, got, c.want)
		}
	}
}

func TestRecordGapAttemptBackoff(t *testing.T) {
	st := mustOpenGap(t)
	base := 5 * time.Minute
	t0 := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	// First look: attempt=1, next = t0 + base (2^0).
	g, err := st.RecordGapAttempt(t0, "stray:redis_memory_used_bytes", "stray", base, GapBackoffCap)
	if err != nil {
		t.Fatal(err)
	}
	if g.AttemptCount != 1 || !g.NextRevisitAt.Equal(t0.Add(base)) {
		t.Fatalf("1st look: attempts=%d next=%s, want 1 / %s", g.AttemptCount, g.NextRevisitAt, t0.Add(base))
	}
	if g.Kind != "stray" {
		t.Fatalf("kind = %q, want stray", g.Kind)
	}
	// Second look at t1: attempt=2, next = t1 + 2*base (2^1).
	t1 := t0.Add(base)
	g, err = st.RecordGapAttempt(t1, "stray:redis_memory_used_bytes", "stray", base, GapBackoffCap)
	if err != nil {
		t.Fatal(err)
	}
	if g.AttemptCount != 2 || !g.NextRevisitAt.Equal(t1.Add(2*base)) {
		t.Fatalf("2nd look: attempts=%d next=%s, want 2 / %s", g.AttemptCount, g.NextRevisitAt, t1.Add(2*base))
	}
	if !g.FirstSeenAt.Equal(t0) {
		t.Fatalf("first_seen drifted: %s, want %s", g.FirstSeenAt, t0)
	}
}

func TestDueGapsBackoffSkips(t *testing.T) {
	st := mustOpenGap(t)
	base := 5 * time.Minute
	t0 := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	ids := []string{"stray:a", "stray:b"}
	// b has never been examined → always due. a is examined at t0 → next = t0+base.
	if _, err := st.RecordGapAttempt(t0, "stray:a", "stray", base, GapBackoffCap); err != nil {
		t.Fatal(err)
	}
	// Just after t0, before a's backoff expires: a NOT due, b due.
	due, err := st.DueGaps(t0.Add(time.Minute), ids)
	if err != nil {
		t.Fatal(err)
	}
	if due["stray:a"] {
		t.Errorf("a should be backing off (not due) 1m after a base-5m attempt")
	}
	if !due["stray:b"] {
		t.Errorf("b never examined → must be due")
	}
	// After a's backoff window: a due again.
	due, err = st.DueGaps(t0.Add(base+time.Second), ids)
	if err != nil {
		t.Fatal(err)
	}
	if !due["stray:a"] {
		t.Errorf("a should be due again once next_revisit passed")
	}
}

func TestRecordGapRejectionLengthensBackoff(t *testing.T) {
	st := mustOpenGap(t)
	base := 5 * time.Minute
	t0 := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	// Two rejections push attempt_count to 2 → next = t0 + 2*base; rejection_count tracked.
	if err := st.RecordGapRejection(t0, "stray:x", "evidence below floor", base, GapBackoffCap); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordGapRejection(t0, "stray:x", "human rejected", base, GapBackoffCap); err != nil {
		t.Fatal(err)
	}
	g, found, err := st.GetGapState("stray:x")
	if err != nil || !found {
		t.Fatalf("get: %v found=%v", err, found)
	}
	if g.AttemptCount != 2 || g.RejectionCount != 2 {
		t.Fatalf("attempts=%d rejections=%d, want 2/2", g.AttemptCount, g.RejectionCount)
	}
	if g.LastReason != "human rejected" {
		t.Fatalf("last reason = %q", g.LastReason)
	}
	if !g.NextRevisitAt.Equal(t0.Add(2 * base)) {
		t.Fatalf("next = %s, want %s", g.NextRevisitAt, t0.Add(2*base))
	}
}

func TestGapAttemptsFeedsRecurrence(t *testing.T) {
	st := mustOpenGap(t)
	base := time.Minute
	t0 := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	if n, _ := st.GapAttempts("stray:none"); n != 0 {
		t.Fatalf("unknown gap attempts = %d, want 0", n)
	}
	for i := 0; i < 3; i++ {
		if _, err := st.RecordGapAttempt(t0.Add(time.Duration(i)*time.Hour), "stray:y", "stray", base, GapBackoffCap); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := st.GapAttempts("stray:y"); n != 3 {
		t.Fatalf("attempts = %d, want 3", n)
	}
}

func TestRecordGapAttemptDeterministic(t *testing.T) {
	base := 3 * time.Minute
	t0 := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	run := func() GapState {
		st := mustOpenGap(t)
		var g GapState
		for i := 0; i < 4; i++ {
			g, _ = st.RecordGapAttempt(t0.Add(time.Duration(i)*time.Minute), "stray:z", "stray", base, GapBackoffCap)
		}
		return g
	}
	a, b := run(), run()
	if a != b {
		t.Fatalf("gap scheduling not deterministic:\n a=%+v\n b=%+v", a, b)
	}
}
