package incident

import (
	"testing"
	"time"
)

// network-free, -race. The deterministic spine of T-B: every test pins a charter or
// correctness property the incident-memory gate will also enforce.

var t0 = time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

const (
	week        = 7 * 24 * time.Hour
	resolveHorz = 90 * time.Second // > the eval tick; a gap beyond this = a new episode
)

// TestKeyPurity: the key is reproducible from its three stated inputs (a learned or
// opaque key would fail this — the charter's deterministic-grouping guarantee).
func TestKeyPurity(t *testing.T) {
	k1 := Key("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", t0)
	k2 := Key("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", t0)
	if k1 != k2 {
		t.Fatal("key is not a pure function of its inputs")
	}
	if len(k1) != 64 {
		t.Fatalf("key is not a sha256 hex digest: len=%d", len(k1))
	}
	// Any field change changes the key.
	if Key("PHEN_OOM_KILL", "r|c|ns|Deployment|web", t0) == k1 {
		t.Error("phenomenon change did not change the key")
	}
	if Key("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|payment", t0) == k1 {
		t.Error("role change did not change the key")
	}
	if Key("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", t0.Add(week)) == k1 {
		t.Error("bucket change did not change the key")
	}
}

// TestGraphVersionNotInKey is the sharpest design edge: a phenomenon-definition bump
// must NOT split a recurring incident. The key has no graph-version field, so the same
// (phen, role, bucket) observed under two graph versions stays ONE incident.
func TestGraphVersionNotInKey(t *testing.T) {
	a := NewAccumulator(resolveHorz, week)
	k1 := a.Observe("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", false, "sha256:v1", t0)
	k2 := a.Observe("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", false, "sha256:v2", t0.Add(5*time.Minute))
	if k1 != k2 {
		t.Fatal("a graph-version bump split a recurring incident — graph version leaked into the key")
	}
	if got := len(a.Snapshot()); got != 1 {
		t.Fatalf("expected 1 incident across a version bump, got %d", got)
	}
}

// TestRecurrenceOnlyAcrossGap: a resolve→refire gap increments the count; a continuous
// run of sub-horizon ticks does NOT (the count-every-tick inflation the gate forbids).
func TestRecurrenceOnlyAcrossGap(t *testing.T) {
	// Continuous: 10 ticks 30s apart (< resolveHorz) ⇒ ONE episode.
	cont := NewAccumulator(resolveHorz, week)
	for i := 0; i < 10; i++ {
		cont.Observe("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", false, "v", t0.Add(time.Duration(i)*30*time.Second))
	}
	if c := cont.Snapshot()[0].RecurrenceCount; c != 1 {
		t.Fatalf("continuous span recurrence = %d, want 1 (no per-tick inflation)", c)
	}

	// Resolve→refire: fire, a 10-minute gap (> resolveHorz), fire again ⇒ count 2.
	rec := NewAccumulator(resolveHorz, week)
	rec.Observe("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", false, "v", t0)
	rec.Observe("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", false, "v", t0.Add(30*time.Second)) // same episode
	rec.Observe("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", false, "v", t0.Add(10*time.Minute)) // new episode
	rec.Observe("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", false, "v", t0.Add(20*time.Minute)) // new episode
	inc := rec.Snapshot()[0]
	if inc.RecurrenceCount != 3 {
		t.Fatalf("recurrence = %d, want 3 (two resolve gaps)", inc.RecurrenceCount)
	}
	if inc.Lifespan != 20*time.Minute {
		t.Fatalf("lifespan = %v, want 20m (last-first)", inc.Lifespan)
	}
}

// TestSeparation: distinct phenomena or roles are distinct incidents — never collapsed.
func TestSeparation(t *testing.T) {
	a := NewAccumulator(resolveHorz, week)
	a.Observe("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", false, "v", t0)
	a.Observe("PHEN_OOM_KILL", "r|c|ns|Deployment|web", false, "v", t0)        // same role, diff phenomenon
	a.Observe("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|payment", false, "v", t0) // same phenomenon, diff role
	if got := len(a.Snapshot()); got != 3 {
		t.Fatalf("expected 3 distinct incidents, got %d (a collapse hides recurrence)", got)
	}
}

// TestBucketBoundary: two episodes straddling a window-bucket edge are TWO incidents —
// the bucket is on the observation, not on a wandering "now".
func TestBucketBoundary(t *testing.T) {
	a := NewAccumulator(resolveHorz, week)
	b := Bucket(t0, week)
	a.Observe("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", false, "v", b.Add(1*time.Hour))      // bucket N
	a.Observe("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", false, "v", b.Add(week+1*time.Hour)) // bucket N+1
	if got := len(a.Snapshot()); got != 2 {
		t.Fatalf("expected 2 incidents across a bucket boundary, got %d", got)
	}
}

// TestRestartInvariance is the load-bearing claim over the existing findings table: an
// incident SURVIVES a process restart. Folding a sequence in one pass must equal folding
// it with a Restore() (rehydrate from the durable store) injected mid-stream.
func TestRestartInvariance(t *testing.T) {
	// Fire, same-episode tick, then a resolve gap + refire ⇒ exactly ONE resolve gap.
	obs := []time.Time{t0, t0.Add(30 * time.Second), t0.Add(10 * time.Minute)}

	straight := NewAccumulator(resolveHorz, week)
	for _, at := range obs {
		straight.Observe("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", false, "v", at)
	}

	// Restart after the 2nd observation: persist, rehydrate into a fresh accumulator.
	before := NewAccumulator(resolveHorz, week)
	before.Observe("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", false, "v", obs[0])
	before.Observe("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", false, "v", obs[1])
	after := NewAccumulator(resolveHorz, week)
	after.Restore(before.Snapshot()) // the durable store hand-off
	after.Observe("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", false, "v", obs[2])

	s, r := straight.Snapshot(), after.Snapshot()
	if len(s) != 1 || len(r) != 1 {
		t.Fatalf("expected 1 incident each, got %d / %d", len(s), len(r))
	}
	if s[0].Key != r[0].Key || s[0].RecurrenceCount != r[0].RecurrenceCount || !s[0].LastSeen.Equal(r[0].LastSeen) {
		t.Fatalf("restart changed the incident: straight=%+v restart=%+v", s[0], r[0])
	}
	if r[0].RecurrenceCount != 2 {
		t.Fatalf("recurrence across restart = %d, want 2", r[0].RecurrenceCount)
	}
}

// TestRoleUnresolvedHonest: an unresolvable role is recorded under the instance key with
// the flag set — never dropped, never assigned a guessed role.
func TestRoleUnresolvedHonest(t *testing.T) {
	a := NewAccumulator(resolveHorz, week)
	a.Observe("PHEN_MEMORY_LEAK", "i|c|ns|Pod|web-x|uid", true, "v", t0)
	s := a.Snapshot()
	if len(s) != 1 || !s[0].RoleUnresolved {
		t.Fatalf("unresolved-role incident not recorded honestly: %+v", s)
	}
}

// TestSnapshotDeterministic: the snapshot is byte-stable (key-ordered) across two
// independent folds of the same sequence — the determinism the replay gate asserts.
func TestSnapshotDeterministic(t *testing.T) {
	build := func() []Incident {
		a := NewAccumulator(resolveHorz, week)
		a.Observe("PHEN_OOM_KILL", "r|c|ns|Deployment|payment", false, "v", t0.Add(time.Minute))
		a.Observe("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", false, "v", t0)
		a.Observe("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", false, "v", t0.Add(10*time.Minute))
		return a.Snapshot()
	}
	s1, s2 := build(), build()
	if len(s1) != len(s2) {
		t.Fatalf("nondeterministic length: %d vs %d", len(s1), len(s2))
	}
	for i := range s1 {
		if s1[i].Key != s2[i].Key || s1[i].RecurrenceCount != s2[i].RecurrenceCount {
			t.Fatalf("nondeterministic snapshot at %d: %+v vs %+v", i, s1[i], s2[i])
		}
	}
}
