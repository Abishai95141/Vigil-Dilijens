package identity

import (
	"testing"
	"time"
)

// Retention/eviction boundary tests for the two-tier tombstone store (doc 14 §1.4).
// (Originated as adversarial edge-case probes; curated and verified here.)

// Eviction is by oldest death (the documented policy, see enforceCapLocked): the
// tombstone that died earliest is shed first, because it is least likely to still
// serve a late sample. Verifies the policy and the EvictedBeforeHorizon signal.
func TestEvictionIsOldestDeath(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := NewStore(clk.Now, 15*time.Minute, 24*time.Hour, 1) // cap 1 tombstone
	role := roleFor("shop", "Deployment", "web")

	cA := podCoords("shop", "pA", "uid-A")
	st.Observe(cA, role, lcBase, StateActive)
	st.TerminateInstance(cA, lcBase.Add(time.Minute)) // dies first (+1m)

	cB := podCoords("shop", "pB", "uid-B")
	st.Observe(cB, role, lcBase, StateActive)
	st.TerminateInstance(cB, lcBase.Add(2*time.Minute)) // dies later -> A (oldest) evicted

	if _, ok := st.PodUID("shop", "pA", lcBase.Add(30*time.Second)); ok {
		t.Error("oldest-died tombstone A should have been evicted under cap pressure")
	}
	if m := st.Metrics(); m.EvictedBeforeHorizon != 1 {
		t.Errorf("EvictedBeforeHorizon = %d, want 1", m.EvictedBeforeHorizon)
	}
}

// A tombstone evicted exactly AT stubRetention age is past its horizon, so it is
// counted Archived (normal end of life), not EvictedBeforeHorizon (premature).
func TestCapEvictionHorizonBoundary(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := NewStore(clk.Now, 15*time.Minute, 24*time.Hour, 1)
	role := roleFor("shop", "Deployment", "web")

	st.Observe(podCoords("shop", "pA", "uid-A"), role, lcBase, StateActive)
	st.TerminateInstance(podCoords("shop", "pA", "uid-A"), lcBase)

	clk.Advance(24 * time.Hour) // A age == exactly stubRetention -> expired

	st.Observe(podCoords("shop", "pB", "uid-B"), role, lcBase.Add(24*time.Hour), StateActive)
	st.TerminateInstance(podCoords("shop", "pB", "uid-B"), lcBase.Add(24*time.Hour)) // evicts A

	m := st.Metrics()
	if m.EvictedBeforeHorizon != 0 || m.Archived != 1 {
		t.Errorf("at-horizon eviction: want EvictedBeforeHorizon 0 / Archived 1, got %d / %d",
			m.EvictedBeforeHorizon, m.Archived)
	}
}

// A late join landing exactly at the full->stub boundage is degraded (15m < 15m is
// false -> stub tier) and counted once.
func TestDegradedJoinAtFullRetentionBoundary(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	c := podCoords("shop", "web-x", "uid-1")
	st.Observe(c, roleFor("shop", "Deployment", "web"), lcBase, StateActive)
	st.TerminateInstance(c, lcBase.Add(5*time.Minute))

	clk.Advance(20 * time.Minute) // age since death == exactly 15m -> stub tier
	if uid, ok := st.PodUID("shop", "web-x", lcBase.Add(time.Minute)); !ok || uid != "uid-1" {
		t.Fatalf("boundary join = (%q,%v), want (uid-1,true)", uid, ok)
	}
	if d := st.Metrics().DegradedJoins; d != 1 {
		t.Errorf("DegradedJoins = %d, want 1 at the full->stub boundary", d)
	}
}

// A sample that does NOT fall within any record's [born,died) interval must not
// resolve and must not bump DegradedJoins (only covering records reach the tier check).
func TestNonCoveringLookupNotCountedDegraded(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	c := podCoords("shop", "web-x", "uid-1")
	st.Observe(c, roleFor("shop", "Deployment", "web"), lcBase, StateActive)
	st.TerminateInstance(c, lcBase.Add(5*time.Minute))
	clk.Advance(20 * time.Minute) // stub tier

	if _, ok := st.PodUID("shop", "web-x", lcBase.Add(6*time.Minute)); ok {
		t.Error("a sample after death must not resolve (half-open interval)")
	}
	if d := st.Metrics().DegradedJoins; d != 0 {
		t.Errorf("non-covering lookup must not count DegradedJoins, got %d", d)
	}
}

// Active = live instances only; an expired-but-not-yet-GC'd tombstone counts in no
// tier and is excluded from Active.
func TestActiveCountWithExpiredNotGCd(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	role := roleFor("shop", "Deployment", "web")
	st.Observe(podCoords("shop", "live", "uid-live"), role, lcBase, StateActive)
	cD := podCoords("shop", "dead", "uid-dead")
	st.Observe(cD, role, lcBase, StateActive)
	st.TerminateInstance(cD, lcBase)

	clk.Advance(25 * time.Hour) // dead is expired but not GC'd
	m := st.Metrics()
	if m.Active != 1 {
		t.Errorf("Active = %d, want 1", m.Active)
	}
	if m.FullTombstones != 0 || m.StubTombstones != 0 {
		t.Errorf("expired tombstone counted in a tier: full=%d stub=%d", m.FullTombstones, m.StubTombstones)
	}
}

// GC archives each expired tombstone exactly once (no double counting between the
// GC sweep and the subsequent cap re-enforcement).
func TestGCDoesNotDoubleCount(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	role := roleFor("shop", "Deployment", "web")
	for i, n := range []string{"a", "b", "c"} {
		c := podCoords("shop", n, "uid-"+n)
		st.Observe(c, role, lcBase.Add(time.Duration(i)*time.Minute), StateActive)
		st.TerminateInstance(c, lcBase.Add(time.Duration(i)*time.Minute))
	}
	clk.Advance(25 * time.Hour)
	st.GC()
	m := st.Metrics()
	if m.Archived != 3 || m.EvictedBeforeHorizon != 0 {
		t.Errorf("want Archived 3 / EvictedBeforeHorizon 0, got %d / %d", m.Archived, m.EvictedBeforeHorizon)
	}
}

// THE trust-critical edge: when cap pressure evicts a dead instance that a late
// sample still needs, the join honestly QUARANTINES (returns not-found) rather than
// silently mis-joining a same-name successor — and the eviction is counted.
func TestCapEvictionQuarantinesNeverMisjoinsSuccessor(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := NewStore(clk.Now, 15*time.Minute, 24*time.Hour, 1) // cap 1
	role := roleFor("shop", "Deployment", "web")

	cOld := podCoords("shop", "web-x", "uid-OLD")
	st.Observe(cOld, role, lcBase, StateActive)
	st.TerminateInstance(cOld, lcBase.Add(time.Minute)) // OLD lives [base, +1m)
	if uid, ok := st.PodUID("shop", "web-x", lcBase.Add(30*time.Second)); !ok || uid != "uid-OLD" {
		t.Fatalf("precondition: late sample should join uid-OLD, got (%q,%v)", uid, ok)
	}

	// A different-name death pushes over the cap and evicts OLD (older died).
	cOther := podCoords("shop", "api-y", "uid-OTHER")
	st.Observe(cOther, role, lcBase, StateActive)
	st.TerminateInstance(cOther, lcBase.Add(2*time.Minute))

	// The same name is reused (the honeypot).
	st.Observe(podCoords("shop", "web-x", "uid-NEW"), role, lcBase.Add(3*time.Minute), StateActive)

	// The late sample for OLD now finds nothing (OLD evicted; sample predates NEW) —
	// an honest quarantine, NOT a mis-join to the successor.
	uid, ok := st.PodUID("shop", "web-x", lcBase.Add(30*time.Second))
	if ok {
		t.Errorf("late sample resolved to %q after its instance was evicted; want not-found (quarantine)", uid)
	}
	if st.Metrics().EvictedBeforeHorizon != 1 {
		t.Errorf("eviction of an in-horizon tombstone must be counted, got %d", st.Metrics().EvictedBeforeHorizon)
	}
}

// A death stamped ahead of the store clock (DeletionTimestamp under skew, doc 14 A12)
// yields a negative age — treated as a full tombstone, and a sample before that death
// still resolves.
func TestFutureDatedDeathResolves(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	c := podCoords("shop", "web-x", "uid-1")
	st.Observe(c, roleFor("shop", "Deployment", "web"), lcBase, StateActive)
	st.TerminateInstance(c, lcBase.Add(10*time.Minute)) // death 10m in the future vs clock

	if uid, ok := st.PodUID("shop", "web-x", lcBase.Add(time.Minute)); !ok || uid != "uid-1" {
		t.Errorf("sample before a future-dated death should resolve, got (%q,%v)", uid, ok)
	}
}

// Under tight cap pressure the just-closed predecessor can itself be evicted during
// the same Observe that linked it, leaving a dangling Predecessor reference. That is
// acceptable (the link is provenance, not a join key): the store stays consistent and
// the current instance resolves unambiguously.
func TestSuccessionPredecessorEvictedStaysConsistent(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := NewStore(clk.Now, 15*time.Minute, 24*time.Hour, 1) // cap 1
	role := roleFor("shop", "Deployment", "web")

	// Occupy the single tombstone slot with a younger-died filler so the older-died
	// predecessor is the one evicted.
	cFiller := podCoords("shop", "filler", "uid-F")
	st.Observe(cFiller, role, lcBase, StateActive)
	st.TerminateInstance(cFiller, lcBase.Add(10*time.Minute))

	st.Observe(podCoords("shop", "web-x", "uid-OLD"), role, lcBase, StateActive)
	st.Observe(podCoords("shop", "web-x", "uid-NEW"), role, lcBase.Add(time.Minute), StateActive)

	newRec, ok := st.Get(mustKey(t, podCoords("shop", "web-x", "uid-NEW")))
	if !ok || !newRec.alive() {
		t.Fatal("new instance should be present and alive")
	}
	if st.Metrics().Successions != 1 {
		t.Errorf("Successions = %d, want 1", st.Metrics().Successions)
	}
	if uid, ok := st.PodUID("shop", "web-x", lcBase.Add(2*time.Minute)); !ok || uid != "uid-NEW" {
		t.Errorf("current instance lookup = (%q,%v), want (uid-NEW,true)", uid, ok)
	}
}
