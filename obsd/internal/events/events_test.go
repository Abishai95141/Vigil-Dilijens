package events

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

const clusterID = "cl"

var t0 = time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

// newStoreWith builds a real identity store holding one role-resolvable pod
// (currency) and one role-less node — so role-resolution is exercised through the
// REAL store, not a stub (the join-fidelity oracle is then the store's own truth).
func newStoreWith(t *testing.T) (*identity.Store, string, string) {
	t.Helper()
	store := identity.NewStore(func() time.Time { return t0.Add(time.Minute) }, time.Hour, 24*time.Hour, 100000)
	role, err := identity.MintRole(identity.RoleCoords{
		Cluster: clusterID, Namespace: "shop", Kind: "Deployment", RoleKey: "Deployment/currency",
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Observe(identity.InstanceCoords{
		Cluster: clusterID, Namespace: "shop", Kind: "Pod", Name: "currency-abc", UID: "uid-pod-1",
	}, role, t0, identity.StateActive); err != nil {
		t.Fatal(err)
	}
	// A node carries no role (zero RoleCEI) — the honest-unresolved path.
	if _, err := store.Observe(identity.InstanceCoords{
		Cluster: clusterID, Kind: "Node", Name: "vigil-worker", UID: "uid-node-1",
	}, identity.CEI{}, t0, identity.StateActive); err != nil {
		t.Fatal(err)
	}
	return store, role.Key(), identity.CEI{
		Layer: identity.LayerInstance, Cluster: clusterID, Namespace: "shop", Kind: "Pod", Name: "currency-abc", UID: "uid-pod-1",
	}.Key()
}

func TestResolveEventRole(t *testing.T) {
	store, roleKey, instKey := newStoreWith(t)

	// A pod the store knows resolves to its durable role CEI.
	ent, role, unresolved := ResolveEventRole(store, clusterID, Involved{Namespace: "shop", Name: "currency-abc", Kind: "Pod", UID: "uid-pod-1"})
	if unresolved {
		t.Fatal("a store-known pod must resolve to a role")
	}
	if ent != instKey || role != roleKey {
		t.Fatalf("resolved wrong: ent=%q role=%q want ent=%q role=%q", ent, role, instKey, roleKey)
	}

	// A pod the store has NOT seen: instance key + unresolved, never a guessed role.
	ent2, role2, unresolved2 := ResolveEventRole(store, clusterID, Involved{Namespace: "shop", Name: "ghost", Kind: "Pod", UID: "uid-ghost"})
	if !unresolved2 || ent2 != role2 {
		t.Fatalf("an unknown pod must be unresolved with role==instance key, got ent=%q role=%q unresolved=%v", ent2, role2, unresolved2)
	}

	// A node (no role layer) is honestly unresolved.
	_, _, nodeUnresolved := ResolveEventRole(store, clusterID, Involved{Name: "vigil-worker", Kind: "Node", UID: "uid-node-1"})
	if !nodeUnresolved {
		t.Error("a role-less Node event must be unresolved (never assigned a workload role)")
	}
}

func conds() []Corroboration {
	return []Corroboration{
		{Reason: "OOMKilled", InvolvedKind: "Pod", Corroborates: "PHEN_OOM_KILL_CGROUP", Role: RoleCorroborating,
			TemporalOrder: "T0", Why: "authored: an OOMKilled event co-occurs with the cgroup-OOM phenomenon on the same role; it corroborates, it does not prove.",
			Author: "vigil-engineering", Version: "v1", Status: "experimental"},
		{Reason: "CrashLoopBackOff", InvolvedKind: "Pod", Corroborates: "", Role: RoleCorroborating,
			TemporalOrder: "T0", Why: "authored: a CrashLoopBackOff is a MEASURED restart-loop fact, visible standalone; no gauge phenomenon corroborates it yet.",
			Author: "vigil-engineering", Version: "v1", Status: "experimental"},
	}
}

func TestCorroborateJoinsOnExactRole(t *testing.T) {
	_, roleKey, instKey := newStoreWith(t)
	evs := []EventFinding{
		{Reason: "OOMKilled", EntityCEI: instKey, RoleCEI: roleKey, Kind: "Pod", Namespace: "shop", Name: "currency-abc", Count: 1},
	}
	// A gauge finding for PHEN_OOM_KILL_CGROUP exists on the SAME role this snapshot.
	gauge := map[string]map[string]bool{"PHEN_OOM_KILL_CGROUP": {roleKey: true}}
	out := Corroborate(evs, gauge, conds())
	if len(out) != 1 {
		t.Fatalf("want 1, got %d", len(out))
	}
	if !out[0].GaugeRoleMatch || out[0].Corroborates != "PHEN_OOM_KILL_CGROUP" || out[0].Why == "" {
		t.Fatalf("expected a corroborated join with the authored why, got %+v", out[0])
	}
}

// TestCorroborateIdentityMismatch is the join-fidelity floor: an event resolved to
// role A must NOT corroborate a gauge finding on role B. The exact-CEI match is the
// guarantee — a single false join here would be a silent identity mis-join.
func TestCorroborateIdentityMismatch(t *testing.T) {
	_, roleKey, instKey := newStoreWith(t)
	evs := []EventFinding{
		{Reason: "OOMKilled", EntityCEI: instKey, RoleCEI: roleKey, Kind: "Pod", Count: 1},
	}
	// The gauge finding is on a DIFFERENT role.
	gauge := map[string]map[string]bool{"PHEN_OOM_KILL_CGROUP": {"r|cl|shop|Deployment|Deployment/cart": true}}
	out := Corroborate(evs, gauge, conds())
	if out[0].GaugeRoleMatch {
		t.Fatal("an event on role A corroborated a gauge finding on role B — identity mis-join")
	}
	if out[0].Why != "" {
		t.Error("a non-joined event must carry no authored why")
	}
}

func TestCorroborateStandaloneVisible(t *testing.T) {
	roleKey := "r|cl|shop|Deployment|Deployment/currency"
	evs := []EventFinding{
		// CrashLoopBackOff: corroborates nothing — must stay visible, never upgraded.
		{Reason: "CrashLoopBackOff", EntityCEI: "i|cl|shop|Pod|p|u", RoleCEI: roleKey, Kind: "Pod", Count: 7},
		// OOMKilled with NO gauge finding on its role: visible, standalone, not joined.
		{Reason: "OOMKilled", EntityCEI: "i|cl|shop|Pod|q|v", RoleCEI: roleKey, Kind: "Pod", Count: 1},
	}
	out := Corroborate(evs, map[string]map[string]bool{}, conds())
	if len(out) != 2 {
		t.Fatalf("both events must be surfaced (standalone-visible), got %d", len(out))
	}
	for _, ce := range out {
		if ce.GaugeRoleMatch {
			t.Errorf("no gauge finding present, yet %s was marked corroborated", ce.Event.Reason)
		}
	}
}

func TestCorroborateUnresolvedNeverJoins(t *testing.T) {
	instKey := "i|cl|shop|Pod|ghost|uid"
	evs := []EventFinding{
		{Reason: "OOMKilled", EntityCEI: instKey, RoleCEI: instKey, RoleUnresolved: true, Kind: "Pod", Count: 1},
	}
	// Even a gauge map keyed on the instance key must not produce a join for an
	// unresolved event (we cannot honestly join it to a workload role).
	gauge := map[string]map[string]bool{"PHEN_OOM_KILL_CGROUP": {instKey: true}}
	out := Corroborate(evs, gauge, conds())
	if out[0].GaugeRoleMatch {
		t.Fatal("a role-unresolved event must never be corroborated")
	}
}

func TestCorroborateDeterministic(t *testing.T) {
	evs := []EventFinding{
		{Reason: "OOMKilled", EntityCEI: "i|cl|shop|Pod|z|9", RoleCEI: "r|cl|shop|Deployment|Deployment/z", Kind: "Pod"},
		{Reason: "CrashLoopBackOff", EntityCEI: "i|cl|shop|Pod|a|1", RoleCEI: "r|cl|shop|Deployment|Deployment/a", Kind: "Pod"},
	}
	a, _ := json.Marshal(Corroborate(evs, nil, conds()))
	b, _ := json.Marshal(Corroborate(evs, nil, conds()))
	if !bytes.Equal(a, b) {
		t.Fatal("Corroborate is not deterministic across two identical calls")
	}
}

func TestReasonsFromConditions(t *testing.T) {
	r := Reasons(conds())
	if len(r) != 2 || r[0] != "CrashLoopBackOff" || r[1] != "OOMKilled" {
		t.Fatalf("Reasons = %v, want sorted [CrashLoopBackOff OOMKilled]", r)
	}
}
