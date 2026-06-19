package identity

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"
)

// Adversarial property layer over the #1 silent failure: the IDENTITY MIS-JOIN
// (doc 03 §3.3, doc 14 §1.4/§3.2). Where the curated *_test.go files pin specific
// honeypot scenarios, this file fuzzes the SHAPE of pod churn — recycled names with
// different UIDs within and across ticks, namespace collisions, late-arriving
// samples — and asserts the structural invariant directly:
//
//	a sample binds to the EXACT instance alive when it was TAKEN, or it quarantines.
//	It NEVER binds to a stale/successor/wrong instance.
//
// These are property tests, not golden tests: the property is checked over many
// pseudo-random histories. Randomness is deterministically seeded (fixed seeds, no
// time.Now), so a failure reproduces byte-for-byte — the determinism guarantee still
// holds. Every generated history is also replayed against an INDEPENDENT oracle
// (the generator's own ground truth), so the test fails loudly if the store ever
// mis-joins; it is not a tautology over the store's own bookkeeping.

// propBase is the fixed epoch for property histories (no wall clock anywhere).
var propBase = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

// generation is one (namespace, name) instance's life in a generated history: a
// distinct UID alive over the EFFECTIVE half-open interval [born, died). A zero died
// means "still alive at end of history". `died` is the effective death the store will
// compute (declared death, or — for a generation with a successor — the successor's
// birth, whichever the succession rule lands on). `declaredDied` is the death event
// actually fed to the store via TerminateInstance (the informer delete); it equals or
// precedes `died` when a delete gap was generated. The oracle keys off `died` (the
// contract); `loadStore` feeds `declaredDied` (the raw events). This is the GROUND
// TRUTH the store must echo.
type generation struct {
	ns, name     string
	uid          string
	born         time.Time
	died         time.Time // effective death; zero => alive
	declaredDied time.Time // the TerminateInstance event time; zero => never explicitly deleted
	// finalDead is true for a generation that died (declaredDied set) but has NO
	// same-name successor to auto-close it. Its terminal death reaches the store ONLY
	// via the explicit delete event — so if that delete is dropped, the store keeps it
	// apparently-alive (a coverage staleness, never a mis-join to a different UID).
	finalDead bool
}

func (g generation) coversInstant(t time.Time) bool {
	if t.Before(g.born) {
		return false
	}
	if g.died.IsZero() {
		return true
	}
	return t.Before(g.died)
}

// churnHistory is a generated, non-overlapping-per-name sequence of generations
// plus the bookkeeping needed to feed both the Store and an oracle.
type churnHistory struct {
	gens []generation
}

// genChurnHistory builds a pseudo-random pod-churn history. Within each (ns,name)
// slot generations strictly succeed one another, modelling the STORE's documented
// succession rule (lifecycle.go Observe): a same-name predecessor (different UID) is
// auto-closed at its SUCCESSOR's birth, so a predecessor's effective life runs right
// up to the next generation's birth — any "delete gap" before a recreate is absorbed
// into the predecessor's tombstone interval, never an empty window. The generator
// therefore records each non-final generation's effective `died` == the next
// generation's `born`, which is exactly what the store will compute. This keeps the
// oracle a faithful, INDEPENDENT model of the contract (it never reads the store);
// the property is then a real check that the store realises that contract, not a
// tautology. Across slots, names are deliberately RECYCLED across namespaces
// (collision bait) and reused with fresh UIDs (the honeypot). UIDs are globally
// unique, so any cross-generation join is provably a mis-join.
//
// A non-final generation's `born` may be SEPARATED from the prior generation's
// declared termination by a delete gap: the gap is loaded into the store as a real
// TerminateInstance at the declared death, then the successor's Observe pulls the
// effective death forward to its own birth — exactly the missed/out-of-order delete
// case the succession rule defends against.
func genChurnHistory(rng *rand.Rand) churnHistory {
	// A small fixed alphabet of names and namespaces so collisions are frequent.
	names := []string{"web", "cart", "redis-0", "api"}
	nss := []string{"shop", "default", "prod"}

	var h churnHistory
	uidSeq := 0
	nextUID := func() string {
		uidSeq++
		return fmt.Sprintf("uid-%04d", uidSeq)
	}

	for _, ns := range nss {
		for _, name := range names {
			// Some slots are empty (the name never exists here) — the namespace-
			// collision case: `web` in `shop` must never answer for `web` in `prod`.
			nGen := rng.Intn(4) // 0..3 generations in this slot
			cursor := propBase.Add(time.Duration(rng.Intn(30)) * time.Minute)
			// First build the births and declared deaths, then fix effective deaths.
			start := len(h.gens)
			for i := 0; i < nGen; i++ {
				life := time.Duration(1+rng.Intn(20)) * time.Minute
				born := cursor
				alive := i == nGen-1 && rng.Intn(2) == 0 // last gen ~half alive
				g := generation{ns: ns, name: name, uid: nextUID(), born: born}
				if !alive {
					g.declaredDied = born.Add(life)
					g.died = g.declaredDied // provisional; fixed to successor birth below
					gap := time.Duration(rng.Intn(3)) * time.Minute
					cursor = g.declaredDied.Add(gap)
				}
				h.gens = append(h.gens, g)
				if alive {
					break
				}
			}
			// Fix effective deaths: a non-final generation in this slot is closed by
			// the store at its successor's birth, so its EFFECTIVE interval ends there.
			for i := start; i < len(h.gens)-1; i++ {
				if h.gens[i].ns == ns && h.gens[i].name == name {
					h.gens[i].died = h.gens[i+1].born
				}
			}
			// The last generation in this slot has no successor; if it declared a
			// death, only the explicit delete event conveys it (finalDead).
			if last := len(h.gens) - 1; last >= start && h.gens[last].ns == ns &&
				h.gens[last].name == name && !h.gens[last].declaredDied.IsZero() {
				h.gens[last].finalDead = true
			}
		}
	}
	return h
}

// oracleUID is the independent ground truth: which UID was (ns,name) at instant t,
// per the generator — NOT per the store. Returns ("", false) for a gap or before
// any generation.
func (h churnHistory) oracleUID(ns, name string, t time.Time) (string, bool) {
	for _, g := range h.gens {
		if g.ns == ns && g.name == name && g.coversInstant(t) {
			return g.uid, true
		}
	}
	return "", false
}

// loadStore replays a churn history into a fresh Store.
//
// Observe events are delivered in BIRTH order — the realistic informer Add ordering
// the store's succession rule is designed around (a predecessor is auto-closed at its
// successor's birth, which is only well-defined when births arrive in order). Delete
// events (declaredDied) are delivered AFTER all observes and in the order `deleteOrd`
// dictates, exercising the store's actual robustness guarantee: missed, late, and
// out-of-order DELETES must not change any join (the predecessor was already closed at
// its successor's birth, so a later/earlier/absent explicit delete is inert).
//
// 100y retention + huge cap: nothing expires or is evicted, so a not-found is a
// genuine gap, never a GC artifact. The clock is parked far in the future so every
// dead generation is past birth but well within retention.
func (h churnHistory) loadStore(clk *fakeClock, deleteOrd []int) *Store {
	st := NewStore(clk.Now, 1000*time.Hour, 1_000_000*time.Hour, 1_000_000)
	role := roleFor("shop", "Deployment", "web")

	births := make([]generation, len(h.gens))
	copy(births, h.gens)
	sort.SliceStable(births, func(i, j int) bool { return births[i].born.Before(births[j].born) })
	for _, g := range births {
		st.Observe(InstanceCoords{Cluster: cluster, Namespace: g.ns, Kind: "Pod", Name: g.name, UID: g.uid}, role, g.born, StateActive)
	}

	// Deletes in the requested order. For a generation that has a successor, the
	// successor's Observe already auto-closed it at the successor's birth, so this
	// explicit (possibly later/earlier) delete is inert — the store never re-opens or
	// re-dates a dead record. For a final dead generation the declared delete is its
	// only death event.
	order := deleteOrd
	if order == nil {
		order = make([]int, len(h.gens))
		for i := range order {
			order[i] = i
		}
	}
	for _, idx := range order {
		g := h.gens[idx]
		if !g.declaredDied.IsZero() {
			st.TerminateInstance(InstanceCoords{Cluster: cluster, Namespace: g.ns, Kind: "Pod", Name: g.name, UID: g.uid}, g.declaredDied)
		}
	}
	return st
}

// sampleInstants returns the set of instants to probe a history at: every birth,
// every death, instants just inside and just outside each life, plus midpoints —
// exactly the boundaries where an off-by-one mis-join would surface.
func (h churnHistory) sampleInstants() []time.Time {
	seen := map[int64]bool{}
	var out []time.Time
	add := func(t time.Time) {
		if !seen[t.UnixNano()] {
			seen[t.UnixNano()] = true
			out = append(out, t)
		}
	}
	for _, g := range h.gens {
		add(g.born.Add(-time.Second))
		add(g.born)
		add(g.born.Add(time.Second))
		if !g.died.IsZero() {
			mid := g.born.Add(g.died.Sub(g.born) / 2)
			add(mid)
			add(g.died.Add(-time.Second))
			add(g.died) // half-open: belongs to successor or nothing, never to g
			add(g.died.Add(time.Second))
		} else {
			add(g.born.Add(time.Hour))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// TestStoreNeverMisjoinsUnderChurn is the core property: across many pseudo-random
// churn histories, the time-aware Store.PodUID resolves EXACTLY the UID the oracle
// says was alive at that instant, or returns not-found. It never returns a different
// UID — that would be the silent mis-join (a stale generation, a same-name successor,
// or a same-name pod in a colliding namespace).
func TestStoreNeverMisjoinsUnderChurn(t *testing.T) {
	for seed := int64(1); seed <= 200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		h := genChurnHistory(rng)
		// Park the clock far past every event so all tombstones stay in horizon.
		clk := newFakeClock(propBase.Add(500 * time.Hour))
		st := h.loadStore(clk, nil)

		for _, g := range h.gens {
			for _, at := range h.sampleInstants() {
				want, wantOK := h.oracleUID(g.ns, g.name, at)
				got, gotOK := st.PodUID(g.ns, g.name, at)

				if gotOK && got != want {
					// The cardinal sin: bound to a UID that was NOT alive at `at`.
					t.Fatalf("seed %d: MIS-JOIN at (%s/%s, %s): store=%q oracle=%q",
						seed, g.ns, g.name, at.Sub(propBase), got, want)
				}
				if gotOK != wantOK {
					// The store and oracle must agree on existence within full
					// retention: every covering generation is resolvable, every gap
					// is a quarantine. (Resolved-but-oracle-empty is itself a mis-join
					// — binding a phantom; oracle-says-alive-but-not-resolved would be
					// lost coverage. Both are failures here.)
					t.Fatalf("seed %d: existence disagreement at (%s/%s, %s): store ok=%v(%q) oracle ok=%v(%q)",
						seed, g.ns, g.name, at.Sub(propBase), gotOK, got, wantOK, want)
				}
			}
		}
	}
}

// TestNamespaceCollisionNeverCrossJoins isolates the namespace-collision axis: the
// SAME (name) lives in two namespaces with disjoint UID timelines. A lookup in one
// namespace must never return the other's UID, at any instant — even when their
// lives overlap exactly. (genChurnHistory mixes this in; this test pins it directly
// so a regression names the collision, not a random seed.)
func TestNamespaceCollisionNeverCrossJoins(t *testing.T) {
	clk := newFakeClock(propBase.Add(500 * time.Hour))
	st := NewStore(clk.Now, 1000*time.Hour, 1_000_000*time.Hour, 1_000_000)
	role := roleFor("shop", "Deployment", "web")

	// Identical name "web", identical lifetimes, different namespaces, different UIDs.
	born := propBase
	for _, c := range []struct{ ns, uid string }{
		{"shop", "uid-shop"},
		{"prod", "uid-prod"},
		{"default", "uid-default"},
	} {
		st.Observe(InstanceCoords{Cluster: cluster, Namespace: c.ns, Kind: "Pod", Name: "web", UID: c.uid}, role, born, StateActive)
	}

	at := born.Add(time.Minute)
	for _, c := range []struct{ ns, want string }{
		{"shop", "uid-shop"},
		{"prod", "uid-prod"},
		{"default", "uid-default"},
	} {
		got, ok := st.PodUID(c.ns, "web", at)
		if !ok || got != c.want {
			t.Errorf("namespace collision: PodUID(%q,web) = (%q,%v), want (%q,true) — cross-namespace mis-join",
				c.ns, got, ok, c.want)
		}
	}
	// A namespace that has NO `web` must resolve to nothing, never to a sibling's UID.
	if got, ok := st.PodUID("kube-system", "web", at); ok {
		t.Errorf("phantom join: PodUID(kube-system,web) = %q, want not-found", got)
	}
}

// TestNormalizerNeverMisjoinsUnderChurn drives the SAME churn histories through the
// cAdvisor ingest (the production join path: no UID label, time-aware lookup keyed on
// the sample's own EventTime). A resolved container/pod CEI must embed EXACTLY the UID
// the oracle says was alive when the sample was TAKEN; a gap sample must quarantine
// as unknown-pod. This proves the normalizer adds no mis-join of its own on top of
// the store — late samples land on the dead generation, never the successor.
func TestNormalizerNeverMisjoinsUnderChurn(t *testing.T) {
	for seed := int64(1); seed <= 120; seed++ {
		rng := rand.New(rand.NewSource(seed))
		h := genChurnHistory(rng)
		clk := newFakeClock(propBase.Add(500 * time.Hour))
		st := h.loadStore(clk, nil)
		// The Store IS a Lookup; wire it straight into the normalizer (the real M2<->M3 join).
		n := NewNormalizer(cluster, st)

		for _, g := range h.gens {
			for _, at := range h.sampleInstants() {
				// A genuinely late sample: RECEIVED far in the future, but TAKEN at
				// `at`. The join must key off EventTime and ignore the receive time —
				// otherwise every late sample mis-joins whatever is current.
				s := Series{
					Family:    FamilyCAdvisor,
					Metric:    "container_cpu_usage_seconds_total",
					Labels:    map[string]string{"namespace": g.ns, "pod": g.name, "container": "app"},
					At:        propBase.Add(1000 * time.Hour), // received "now", long after the sample
					EventTime: at,                             // taken at `at`
				}
				r := n.Normalize(s)
				want, wantOK := h.oracleUID(g.ns, g.name, at)

				switch r.Outcome {
				case OutcomeResolved:
					if !wantOK {
						t.Fatalf("seed %d: PHANTOM JOIN at (%s/%s, %s): resolved CEI %q but oracle says no pod",
							seed, g.ns, g.name, at.Sub(propBase), r.CEI.Key())
					}
					// container CEI UID is "<podUID>/<container>"; it must embed the
					// EXACT generation alive at the event time.
					wantCEIUID := want + "/app"
					if r.CEI.UID != wantCEIUID {
						t.Fatalf("seed %d: MIS-JOIN at (%s/%s, %s): CEI uid=%q, want %q",
							seed, g.ns, g.name, at.Sub(propBase), r.CEI.UID, wantCEIUID)
					}
					if r.CEI.Namespace != g.ns || r.CEI.Name != "app" || r.CEI.Kind != "Container" {
						t.Fatalf("seed %d: resolved CEI coordinates drifted: %+v", seed, r.CEI)
					}
				case OutcomeQuarantined:
					if wantOK {
						t.Fatalf("seed %d: LOST COVERAGE at (%s/%s, %s): quarantined %q but oracle says pod %q was alive",
							seed, g.ns, g.name, at.Sub(propBase), r.Reason, want)
					}
					if r.Reason != ReasonUnknownPod {
						t.Fatalf("seed %d: gap sample quarantined for %q, want %q", seed, r.Reason, ReasonUnknownPod)
					}
					if r.CEI.Kind != "" {
						t.Fatalf("seed %d: quarantined result carried a CEI: %q", seed, r.CEI.Key())
					}
				default:
					t.Fatalf("seed %d: unexpected outcome %s for a named-container row", seed, r.Outcome)
				}
			}
		}
	}
}

// liveFinalUID returns the UID of a final-dead generation whose declared death the
// store cannot know without an explicit delete event, IF instant `at` falls in the
// window [declaredDied, +inf) where a no-delete store would still report it alive —
// i.e. the generation is final-dead, has no successor covering `at`, and at >= born.
// Used only to bound the missed-delete coverage staleness; it is NOT a mis-join.
func (h churnHistory) liveFinalUID(ns, name string, at time.Time) (string, bool) {
	for _, g := range h.gens {
		if g.ns == ns && g.name == name && g.finalDead && !at.Before(g.born) {
			// No successor exists for a final generation, so once at >= born the
			// no-delete store reports g alive forever.
			return g.uid, true
		}
	}
	return "", false
}

// TestDeleteEventRobustnessNoMisjoin pins the store's actual robustness guarantee
// (lifecycle.go Observe): once a predecessor is auto-closed at its successor's birth,
// the explicit DELETE events are INERT for join correctness — their order and lateness
// cannot change any join, and a MISSED delete can only leave a FINAL generation (one
// with no same-name successor) apparently-alive past its death. It can NEVER bind a
// different UID — that would be the mis-join. Three stores share identical birth-
// ordered observes but differ on deletes: (A) deletes in order, (B) deletes reversed,
// (C) NO deletes at all. A and B must match the oracle exactly; C must match A except
// it may over-extend a final-dead generation's life, and even then with that
// generation's OWN uid — never a wrong one.
func TestDeleteEventRobustnessNoMisjoin(t *testing.T) {
	for seed := int64(1); seed <= 100; seed++ {
		rng := rand.New(rand.NewSource(seed))
		h := genChurnHistory(rng)

		fwd := make([]int, len(h.gens))
		rev := make([]int, len(h.gens))
		for i := range fwd {
			fwd[i] = i
			rev[len(h.gens)-1-i] = i
		}
		empty := []int{} // deliver NO delete events at all

		clkA := newFakeClock(propBase.Add(500 * time.Hour))
		clkB := newFakeClock(propBase.Add(500 * time.Hour))
		clkC := newFakeClock(propBase.Add(500 * time.Hour))
		stA := h.loadStore(clkA, fwd)
		stB := h.loadStore(clkB, rev)
		stC := h.loadStore(clkC, empty)

		for _, g := range h.gens {
			for _, at := range h.sampleInstants() {
				a, aok := stA.PodUID(g.ns, g.name, at)
				b, bok := stB.PodUID(g.ns, g.name, at)
				c, cok := stC.PodUID(g.ns, g.name, at)
				want, wantOK := h.oracleUID(g.ns, g.name, at)

				// Deletes delivered (any order) ⇒ exact contract match.
				if a != want || aok != wantOK {
					t.Fatalf("seed %d: delete-order(fwd) diverged from contract at (%s/%s, %s): got=(%q,%v) want=(%q,%v)",
						seed, g.ns, g.name, at.Sub(propBase), a, aok, want, wantOK)
				}
				if a != b || aok != bok {
					t.Fatalf("seed %d: delete ORDER changed a join at (%s/%s, %s): fwd=(%q,%v) rev=(%q,%v)",
						seed, g.ns, g.name, at.Sub(propBase), a, aok, b, bok)
				}

				// No deletes (C): either identical to A, or — only when A is not-found
				// because a FINAL generation's death is unknown without its delete — C
				// reports that final generation's OWN uid alive. Any other divergence,
				// and especially any DIFFERENT uid, is a mis-join.
				if c == a && cok == aok {
					continue
				}
				finalUID, hasFinal := h.liveFinalUID(g.ns, g.name, at)
				if !(cok && !aok && hasFinal && c == finalUID) {
					t.Fatalf("seed %d: missed-delete changed a join beyond a final-generation over-extension at (%s/%s, %s): none=(%q,%v) deletes=(%q,%v) finalUID=(%q,%v)",
						seed, g.ns, g.name, at.Sub(propBase), c, cok, a, aok, finalUID, hasFinal)
				}
			}
		}
	}
}

// --- adversarial OwnerReference succession chains -----------------------------

// genOwnerChain builds a pseudo-random ownerReference chain: random kinds, possible
// cycles (an owner kind that resolves back to an earlier one), multiple
// controller=true entries, non-controller noise, and arbitrary depth. The role
// derivation must never panic, must terminate, and must anchor deterministically.
func genOwnerChain(rng *rand.Rand) (podOwners []OwnerRef, resolver fakeResolver) {
	kinds := []string{"ReplicaSet", "Job", "Deployment", "CronJob", "StatefulSet", "DaemonSet", "FluxKustomization", "Rollout"}
	resolver = fakeResolver{m: map[string]OwnerRef{}}

	// Pod's own owners: 0..3, at most-ish one controller (but sometimes more, to bait).
	nOwners := rng.Intn(4)
	for i := 0; i < nOwners; i++ {
		k := kinds[rng.Intn(len(kinds))]
		podOwners = append(podOwners, OwnerRef{
			Kind:       k,
			Name:       fmt.Sprintf("%s-%d", k, rng.Intn(3)),
			UID:        fmt.Sprintf("uid-%d", rng.Intn(100)),
			Controller: rng.Intn(2) == 0,
		})
	}
	// Resolver entries, including potential cycles among ReplicaSet/Job (the only
	// kinds ResolveChain walks further). Map a handful of (kind|ns|name) keys to
	// random controllers, sometimes pointing back to a resolvable kind.
	for i := 0; i < rng.Intn(6); i++ {
		fromKind := []string{"ReplicaSet", "Job"}[rng.Intn(2)]
		key := fmt.Sprintf("%s|shop|%s-%d", fromKind, fromKind, rng.Intn(3))
		toKind := kinds[rng.Intn(len(kinds))]
		resolver.m[key] = OwnerRef{
			Kind:       toKind,
			Name:       fmt.Sprintf("%s-%d", toKind, rng.Intn(3)),
			UID:        fmt.Sprintf("ruid-%d", rng.Intn(100)),
			Controller: true,
		}
	}
	return podOwners, resolver
}

// TestOwnerChainResolutionTerminatesAndIsDeterministic fuzzes the succession-chain
// resolver. Invariants under any adversarial chain (cycles, deep nesting, multiple
// controllers, noise):
//   - never panics;
//   - the resolved chain length is bounded by the depth cap (no infinite walk);
//   - role derivation is DETERMINISTIC (same inputs => same RoleKey) and SEPARATOR-
//     CLEAN (so the minted role CEI key stays unambiguous);
//   - a chain with no controller is bare; one with a controller is not.
func TestOwnerChainResolutionTerminatesAndIsDeterministic(t *testing.T) {
	for seed := int64(1); seed <= 500; seed++ {
		rng := rand.New(rand.NewSource(seed))
		podOwners, resolver := genOwnerChain(rng)

		chain := ResolveChain("shop", "p", podOwners, resolver)
		if len(chain) > 1+maxChainDepth {
			t.Fatalf("seed %d: chain length %d exceeds depth cap %d (non-terminating walk)",
				seed, len(chain), 1+maxChainDepth)
		}

		role := DeriveRole(cluster, "shop", "p", chain)

		// Determinism: re-resolving identical inputs yields an identical role.
		chain2 := ResolveChain("shop", "p", podOwners, resolver)
		role2 := DeriveRole(cluster, "shop", "p", chain2)
		if role.RoleKey != role2.RoleKey || role.Bare != role2.Bare || role.Kind != role2.Kind {
			t.Fatalf("seed %d: role derivation non-deterministic: %+v vs %+v", seed, role, role2)
		}

		// Bareness matches the presence of a controller in the pod's own owners.
		_, hasController := controllerRef(podOwners)
		if role.Bare != !hasController {
			t.Fatalf("seed %d: Bare=%v but pod hasController=%v", seed, role.Bare, hasController)
		}

		// The minted role CEI must be valid (separator-clean coordinates) — a chain
		// whose names somehow carried the separator would otherwise corrupt the join
		// key. Our generator never injects one, so minting must succeed; this asserts
		// the role coordinates DeriveRole produced are themselves key-safe.
		if _, err := MintRole(role, propBase); err != nil {
			t.Fatalf("seed %d: DeriveRole produced an unmintable role %+v: %v", seed, role, err)
		}
	}
}

// TestOwnerChainTopmostControllerIsAnchor pins the role-stability invariant against
// random INTERMEDIATE churn: regardless of how the lower controllers in a chain
// change, as long as the TOPMOST controller is fixed, the role is fixed. This is what
// keeps a role stable across Deployment rollouts (the ReplicaSet churns, the role
// does not) — an order/intermediate-sensitive anchor would be a role mis-join.
func TestOwnerChainTopmostControllerIsAnchor(t *testing.T) {
	for seed := int64(1); seed <= 200; seed++ {
		rng := rand.New(rand.NewSource(seed))

		// Fixed top controller, random churning intermediate controllers below it.
		top := OwnerRef{Kind: "Deployment", Name: "web", UID: "dep-fixed", Controller: true}
		build := func() []OwnerRef {
			n := 1 + rng.Intn(4)
			chain := make([]OwnerRef, 0, n+1)
			for i := 0; i < n; i++ {
				chain = append(chain, OwnerRef{
					Kind:       "ReplicaSet",
					Name:       fmt.Sprintf("web-%d", rng.Intn(1000)), // churns every rollout
					UID:        fmt.Sprintf("rs-%d", rng.Intn(1000)),
					Controller: true,
				})
			}
			return append(chain, top) // topmost is always the fixed Deployment
		}

		a := DeriveRole(cluster, "shop", "web-1", build())
		b := DeriveRole(cluster, "shop", "web-2", build())
		if a.RoleKey != "Deployment/web" || b.RoleKey != "Deployment/web" {
			t.Fatalf("seed %d: role drifted off the fixed top controller: a=%q b=%q", seed, a.RoleKey, b.RoleKey)
		}
		if a.RoleKey != b.RoleKey {
			t.Fatalf("seed %d: intermediate churn changed the role: %q vs %q", seed, a.RoleKey, b.RoleKey)
		}
	}
}
