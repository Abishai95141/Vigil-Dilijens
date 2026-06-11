package identity

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually-advanced clock for deterministic retention tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{t: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var lcBase = time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC)

func podCoords(ns, name, uid string) InstanceCoords {
	return InstanceCoords{Cluster: cluster, Namespace: ns, Kind: "Pod", Name: name, UID: uid}
}

func roleFor(ns, kind, name string) CEI {
	r, _ := MintRole(RoleCoords{Cluster: cluster, Namespace: ns, Kind: kind, RoleKey: kind + "/" + name}, lcBase)
	return r
}

func newTestStore(clk *fakeClock) *Store {
	return NewStore(clk.Now, 15*time.Minute, 24*time.Hour, 1000)
}

// ActiveInstances must return exactly the alive instances, carry each one's role
// (the join key the inventory groups by), and be a snapshot that a later termination
// cannot retroactively mutate.
func TestActiveInstancesSnapshotAndRoles(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	st.Observe(podCoords("shop", "web-a", "uid-a"), roleFor("shop", "Deployment", "web"), lcBase, StateActive)
	st.Observe(podCoords("shop", "web-b", "uid-b"), roleFor("shop", "Deployment", "web"), lcBase, StateActive)
	st.Observe(podCoords("shop", "cart-a", "uid-c"), roleFor("shop", "Deployment", "cart"), lcBase, StateActive)

	active := st.ActiveInstances()
	if len(active) != 3 {
		t.Fatalf("ActiveInstances len = %d, want 3", len(active))
	}
	byRole := map[string]int{}
	for _, r := range active {
		if !r.alive() {
			t.Errorf("ActiveInstances returned a dead record: %+v", r)
		}
		byRole[r.RoleCEI.RoleKey]++
	}
	if byRole["Deployment/web"] != 2 || byRole["Deployment/cart"] != 1 {
		t.Errorf("role grouping = %v, want web:2 cart:1", byRole)
	}

	// Terminating an instance must not mutate a previously-taken snapshot, and a fresh
	// snapshot must drop the dead one.
	st.TerminateInstance(podCoords("shop", "cart-a", "uid-c"), lcBase.Add(time.Minute))
	if len(active) != 3 {
		t.Errorf("prior snapshot mutated after termination: len = %d, want 3", len(active))
	}
	if got := len(st.ActiveInstances()); got != 2 {
		t.Errorf("post-termination ActiveInstances len = %d, want 2", got)
	}
}

func TestObserveAndLookupAlive(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	if _, err := st.Observe(podCoords("shop", "web-x", "uid-1"), roleFor("shop", "Deployment", "web"), lcBase, StateActive); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	uid, ok := st.PodUID("shop", "web-x", lcBase.Add(time.Minute))
	if !ok || uid != "uid-1" {
		t.Errorf("PodUID = (%q, %v), want (uid-1, true)", uid, ok)
	}
	// Before birth → not found.
	if _, ok := st.PodUID("shop", "web-x", lcBase.Add(-time.Minute)); ok {
		t.Error("lookup before birth should not resolve")
	}
	// Unknown name → not found.
	if _, ok := st.PodUID("shop", "ghost", lcBase.Add(time.Minute)); ok {
		t.Error("unknown pod should not resolve")
	}
	if m := st.Metrics(); m.Active != 1 || m.Discovered != 1 {
		t.Errorf("metrics = %+v, want Active 1 Discovered 1", m)
	}
}

func TestTombstoneTiersAndHorizons(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	st.Observe(podCoords("shop", "web-x", "uid-1"), roleFor("shop", "Deployment", "web"), lcBase, StateActive)

	clk.Advance(5 * time.Minute)
	died := clk.Now() // lcBase+5m
	st.TerminateInstance(podCoords("shop", "web-x", "uid-1"), died)

	sample := lcBase.Add(time.Minute) // taken while alive, in [born, died)

	// Full tier: dead 5 min (< 15 min), late sample still joins, not degraded.
	clk.Advance(5 * time.Minute) // now lcBase+10m, age 5m
	if uid, ok := st.PodUID("shop", "web-x", sample); !ok || uid != "uid-1" {
		t.Errorf("full-tier late join = (%q,%v), want (uid-1,true)", uid, ok)
	}
	if st.Metrics().DegradedJoins != 0 {
		t.Errorf("full-tier join should not be degraded")
	}

	// Stub tier: age 20 min (>15m, <24h), still joins, counted degraded.
	clk.Advance(15 * time.Minute) // now lcBase+25m, age 20m
	if uid, ok := st.PodUID("shop", "web-x", sample); !ok || uid != "uid-1" {
		t.Errorf("stub-tier late join = (%q,%v), want (uid-1,true)", uid, ok)
	}
	if st.Metrics().DegradedJoins != 1 {
		t.Errorf("stub-tier join should count as degraded, got %d", st.Metrics().DegradedJoins)
	}

	// A sample timestamped AFTER death never joins (interval is half-open).
	if _, ok := st.PodUID("shop", "web-x", died.Add(time.Second)); ok {
		t.Error("sample after death must not join")
	}

	// Expired: past stubRetention, no join even before GC reclaims it.
	clk.Advance(24 * time.Hour) // age > 24h
	if _, ok := st.PodUID("shop", "web-x", sample); ok {
		t.Error("expired tombstone must not resolve")
	}
}

func TestSuccessionHoneypotAtStore(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	role := roleFor("shop", "Deployment", "web")

	born1 := lcBase
	born2 := lcBase.Add(10 * time.Minute)
	st.Observe(podCoords("shop", "web-x", "uid-OLD"), role, born1, StateActive)
	clk.Advance(10 * time.Minute) // wall clock now at born2; old still within horizon
	st.Observe(podCoords("shop", "web-x", "uid-NEW"), role, born2, StateActive)

	// Succession recorded; old auto-closed at the successor's birth.
	if m := st.Metrics(); m.Successions != 1 {
		t.Errorf("successions = %d, want 1", m.Successions)
	}
	oldKey := mustKey(t, podCoords("shop", "web-x", "uid-OLD"))
	newKey := mustKey(t, podCoords("shop", "web-x", "uid-NEW"))
	oldRec, _ := st.Get(oldKey)
	newRec, _ := st.Get(newKey)
	if oldRec.alive() || !oldRec.DiedAt.Equal(born2) {
		t.Errorf("old record should be dead at born2, got alive=%v died=%v", oldRec.alive(), oldRec.DiedAt)
	}
	if oldRec.Successor != newKey || newRec.Predecessor != oldKey {
		t.Errorf("succession links wrong: old.Successor=%q new.Predecessor=%q", oldRec.Successor, newRec.Predecessor)
	}

	// Time-aware lookups land on the right instance — the honeypot, at the store.
	if uid, _ := st.PodUID("shop", "web-x", lcBase.Add(5*time.Minute)); uid != "uid-OLD" {
		t.Errorf("sample in old's life joined %q, want uid-OLD", uid)
	}
	if uid, _ := st.PodUID("shop", "web-x", born2.Add(time.Minute)); uid != "uid-NEW" {
		t.Errorf("sample in new's life joined %q, want uid-NEW", uid)
	}
	// Exactly at the boundary belongs to the successor (half-open).
	if uid, _ := st.PodUID("shop", "web-x", born2); uid != "uid-NEW" {
		t.Errorf("boundary instant joined %q, want uid-NEW", uid)
	}
}

func TestNoResurrection(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	st.Observe(podCoords("shop", "web-x", "uid-1"), roleFor("shop", "Deployment", "web"), lcBase, StateActive)
	st.TerminateInstance(podCoords("shop", "web-x", "uid-1"), lcBase.Add(time.Minute))
	// A late Add for the same UID must not bring it back to life.
	st.Observe(podCoords("shop", "web-x", "uid-1"), roleFor("shop", "Deployment", "web"), lcBase, StateActive)
	rec, _ := st.Get(mustKey(t, podCoords("shop", "web-x", "uid-1")))
	if rec.alive() {
		t.Error("dead instance was resurrected by a late Add")
	}
	if st.Metrics().Discovered != 1 {
		t.Errorf("idempotent Observe double-counted: Discovered=%d", st.Metrics().Discovered)
	}
}

func TestIdempotentStateAdvance(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	st.Observe(podCoords("shop", "web-x", "uid-1"), roleFor("shop", "Deployment", "web"), lcBase, StateDiscovered)
	st.Observe(podCoords("shop", "web-x", "uid-1"), roleFor("shop", "Deployment", "web"), lcBase, StateActive)
	rec, _ := st.Get(mustKey(t, podCoords("shop", "web-x", "uid-1")))
	if rec.State != StateActive {
		t.Errorf("state = %s, want active", rec.State)
	}
	if st.Metrics().Discovered != 1 {
		t.Errorf("Discovered = %d, want 1 (idempotent)", st.Metrics().Discovered)
	}
}

func TestLRUCapEvictsBeforeHorizon(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := NewStore(clk.Now, 15*time.Minute, 24*time.Hour, 2) // hard cap 2 tombstones
	role := roleFor("shop", "Deployment", "web")
	for i := 0; i < 5; i++ {
		c := podCoords("shop", fmt.Sprintf("p%d", i), fmt.Sprintf("uid-%d", i))
		st.Observe(c, role, lcBase, StateActive)
		st.TerminateInstance(c, lcBase) // all die at lcBase (age 0, within horizon)
	}
	m := st.Metrics()
	if m.EvictedBeforeHorizon != 3 {
		t.Errorf("EvictedBeforeHorizon = %d, want 3 (5 deaths, cap 2)", m.EvictedBeforeHorizon)
	}
	if m.FullTombstones+m.StubTombstones != 2 {
		t.Errorf("live tombstones = %d, want 2 (cap)", m.FullTombstones+m.StubTombstones)
	}
}

func TestGCEvictsPastStubRetention(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	c := podCoords("shop", "web-x", "uid-1")
	st.Observe(c, roleFor("shop", "Deployment", "web"), lcBase, StateActive)
	st.TerminateInstance(c, lcBase)

	clk.Advance(25 * time.Hour) // past stubRetention
	st.GC()

	m := st.Metrics()
	if m.FullTombstones+m.StubTombstones != 0 {
		t.Errorf("expired tombstone not reclaimed: %+v", m)
	}
	if m.Archived < 1 {
		t.Errorf("Archived = %d, want >= 1", m.Archived)
	}
	if _, ok := st.Get(mustKey(t, c)); ok {
		t.Error("expired record still present after GC")
	}
}

func TestNodeLifecycle(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	node := InstanceCoords{Cluster: cluster, Kind: "Node", Name: "worker-1", UID: "node-uid-1"}
	st.Observe(node, CEI{}, lcBase, StateActive)
	if uid, ok := st.NodeUID("worker-1", lcBase.Add(time.Minute)); !ok || uid != "node-uid-1" {
		t.Errorf("NodeUID = (%q,%v), want (node-uid-1,true)", uid, ok)
	}
	// Node replaced with same name, new UID → succession, time-aware lookup.
	clk.Advance(time.Hour)
	st.Observe(InstanceCoords{Cluster: cluster, Kind: "Node", Name: "worker-1", UID: "node-uid-2"}, CEI{}, lcBase.Add(time.Hour), StateActive)
	if uid, _ := st.NodeUID("worker-1", lcBase.Add(30*time.Minute)); uid != "node-uid-1" {
		t.Errorf("old node sample joined %q, want node-uid-1", uid)
	}
	if uid, _ := st.NodeUID("worker-1", lcBase.Add(2*time.Hour)); uid != "node-uid-2" {
		t.Errorf("new node sample joined %q, want node-uid-2", uid)
	}
}

func TestConcurrentObserveAndLookup(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	role := roleFor("shop", "Deployment", "web")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := podCoords("shop", fmt.Sprintf("p%d", i), fmt.Sprintf("uid-%d", i))
			st.Observe(c, role, lcBase, StateActive)
			st.PodUID("shop", fmt.Sprintf("p%d", i), lcBase.Add(time.Minute))
			st.Metrics()
		}(i)
	}
	wg.Wait()
	if st.Metrics().Active != 50 {
		t.Errorf("Active = %d, want 50", st.Metrics().Active)
	}
}

// A role that first resolved degraded (e.g. ReplicaSet anchor because the
// Deployment was not yet in cache) must self-correct when re-observed with the
// resolved role — the true role is immutable, so a change is a correction.
func TestRoleSelfCorrectsOnReObserve(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	c := podCoords("shop", "web-x", "uid-1")

	st.Observe(c, roleFor("shop", "ReplicaSet", "web-abc"), lcBase, StateActive) // degraded
	rec, _ := st.Get(mustKey(t, c))
	if rec.RoleCEI.RoleKey != "ReplicaSet/web-abc" {
		t.Fatalf("initial role = %q, want degraded ReplicaSet/web-abc", rec.RoleCEI.RoleKey)
	}

	st.Observe(c, roleFor("shop", "Deployment", "web"), lcBase, StateActive) // resync, corrected
	rec, _ = st.Get(mustKey(t, c))
	if rec.RoleCEI.RoleKey != "Deployment/web" {
		t.Errorf("role not corrected on re-observe: %q", rec.RoleCEI.RoleKey)
	}
	if st.Metrics().Discovered != 1 {
		t.Errorf("re-observe double-counted: Discovered=%d", st.Metrics().Discovered)
	}
}

// Rapid same-name recreates under a tiny cap exercise the succession path while LRU
// eviction mutates the byName slice. The join must stay unambiguous: exactly one
// live instance, every prior one closed as a succession.
func TestSuccessionUnderCapPressure(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := NewStore(clk.Now, 15*time.Minute, 24*time.Hour, 2) // tiny cap forces eviction
	role := roleFor("shop", "Deployment", "web")
	for i := 0; i < 6; i++ {
		c := podCoords("shop", "web-x", fmt.Sprintf("uid-%d", i))
		st.Observe(c, role, lcBase.Add(time.Duration(i)*time.Minute), StateActive)
	}
	m := st.Metrics()
	if m.Active != 1 {
		t.Errorf("Active = %d, want 1 (a single live same-name instance)", m.Active)
	}
	if m.Successions != 5 {
		t.Errorf("Successions = %d, want 5", m.Successions)
	}
	if uid, ok := st.PodUID("shop", "web-x", lcBase.Add(10*time.Minute)); !ok || uid != "uid-5" {
		t.Errorf("current lookup = (%q,%v), want (uid-5,true)", uid, ok)
	}
}

func mustKey(t *testing.T, c InstanceCoords) string {
	t.Helper()
	cei, err := MintInstance(c, lcBase)
	if err != nil {
		t.Fatalf("MintInstance: %v", err)
	}
	return cei.Key()
}
