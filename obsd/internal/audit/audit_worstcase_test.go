package audit

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
)

// pipeline runs the pure core (ParseEvents -> Resolve -> Hypothesize) the runtime loop
// is a thin mapper around. It is the unit-under-test for every determinism/charter
// guarantee here: same lines + same resolver + same incident => byte-identical output.
func pipeline(lines []string, inc Incident, lb time.Duration) ([]ChangeEvent, []candidate.Candidate) {
	ch := Resolve(ParseEvents(lines), testResolver)
	return ch, Hypothesize(ch, inc, lb)
}

func sampleIncident() Incident {
	return Incident{ID: "inc-1", RoleCEI: incidentRole, Namespace: "erpnext", Onset: onset, GraphVersion: "vtest"}
}

// shuffled returns a copy of lines permuted by the seeded PRNG. Deterministic given the
// seed (no wall clock): the test is reproducible and hermetic.
func shuffled(lines []string, seed int64) []string {
	out := make([]string, len(lines))
	copy(out, lines)
	r := rand.New(rand.NewSource(seed))
	r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// TestParseEventsShuffleInvariant is stronger than the existing reverse-order check: it
// asserts ParseEvents is invariant to ANY permutation of the input lines, across many
// seeded shuffles. ParseEvents dedups by auditID and sorts by (timestamp, auditID), so
// the order lines ARRIVE in (the only thing the live collector is non-deterministic
// about) must never reach the output. A regression that returned events in arrival order
// (e.g. dropping the final sort, or last-write-wins dedup) is caught here.
func TestParseEventsShuffleInvariant(t *testing.T) {
	lines := fixtureLines(t)
	want := ParseEvents(lines)
	if len(want) == 0 {
		t.Fatal("fixture parsed to zero events; the invariance check would be vacuous")
	}
	for seed := int64(1); seed <= 64; seed++ {
		got := ParseEvents(shuffled(lines, seed))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ParseEvents not invariant to input order (seed=%d):\n got=%+v\nwant=%+v", seed, got, want)
		}
	}
}

// TestFullPipelineShuffleInvariant carries the invariance through the whole core path
// (ParseEvents -> Resolve -> Hypothesize): the MEASURED change events AND the staged
// direction-free hypotheses are byte-identical under any line permutation. This is the
// replay/determinism guarantee for the audit lane stated in doc.go.
func TestFullPipelineShuffleInvariant(t *testing.T) {
	lines := fixtureLines(t)
	inc := sampleIncident()
	wantCh, wantH := pipeline(lines, inc, lookback)
	if len(wantH) == 0 {
		t.Fatal("baseline produced zero hypotheses; the invariance check would be vacuous")
	}
	for seed := int64(101); seed <= 148; seed++ {
		gotCh, gotH := pipeline(shuffled(lines, seed), inc, lookback)
		if !reflect.DeepEqual(gotCh, wantCh) {
			t.Fatalf("change events vary with input order (seed=%d)", seed)
		}
		if !reflect.DeepEqual(gotH, wantH) {
			t.Fatalf("hypotheses vary with input order (seed=%d):\n got=%v\nwant=%v",
				seed, subjects(gotH), subjects(wantH))
		}
	}
}

// TestPipelineRepeatableAcrossRuns: repeated identical runs are byte-identical (no map
// iteration order, no time.Now, no PRNG leaking into the output). This is the "same
// inputs => identical output" floor; if any nondeterminism (e.g. ranging a map without a
// sort) crept in, repeated runs would eventually differ.
func TestPipelineRepeatableAcrossRuns(t *testing.T) {
	lines := fixtureLines(t)
	inc := sampleIncident()
	ch0, h0 := pipeline(lines, inc, lookback)
	for i := 0; i < 50; i++ {
		ch, h := pipeline(lines, inc, lookback)
		if !reflect.DeepEqual(ch, ch0) || !reflect.DeepEqual(h, h0) {
			t.Fatalf("run %d differs from run 0 (nondeterministic core)", i)
		}
	}
}

// ---- Arrow of time: an antecedent must PRECEDE its incident (temporal adjacency only) ----

// TestAntecedentsBoundaryExclusiveOnset: the window is [onset-lookback, onset) — the
// onset instant itself is EXCLUDED (a change AT onset did not precede it). This pins the
// half-open boundary; a regression to a closed interval (<=) would let a simultaneous
// change masquerade as an antecedent.
func TestAntecedentsBoundaryExclusiveOnset(t *testing.T) {
	lb := time.Hour
	atOnset := ChangeEvent{AuditID: "at", Timestamp: onset}
	justBefore := ChangeEvent{AuditID: "before", Timestamp: onset.Add(-time.Nanosecond)}
	atLowerEdge := ChangeEvent{AuditID: "edge", Timestamp: onset.Add(-lb)} // inclusive lower bound
	justOutside := ChangeEvent{AuditID: "outside", Timestamp: onset.Add(-lb - time.Nanosecond)}
	afterOnset := ChangeEvent{AuditID: "after", Timestamp: onset.Add(time.Second)}

	in := []ChangeEvent{atLowerEdge, justBefore, atOnset, afterOnset, justOutside}
	got := Antecedents(in, onset, lb)
	gotIDs := map[string]bool{}
	for _, c := range got {
		gotIDs[c.AuditID] = true
		if !c.Timestamp.Before(onset) {
			t.Errorf("ARROW-OF-TIME BREACH: %q (ts=%v) is at/after onset but survived the prune", c.AuditID, c.Timestamp)
		}
	}
	if !gotIDs["before"] {
		t.Error("a change just before onset must be an antecedent")
	}
	if !gotIDs["edge"] {
		t.Error("the inclusive lower edge (onset-lookback) must be an antecedent")
	}
	if gotIDs["at"] {
		t.Error("a change AT onset must be pruned (onset is exclusive)")
	}
	if gotIDs["after"] {
		t.Error("a change after onset must be pruned (it cannot precede the incident)")
	}
	if gotIDs["outside"] {
		t.Error("a change older than the lookback lower bound must be excluded")
	}
}

// TestAntecedentsArrowIsPureFilterNotCausal: Antecedents only PRUNES on time, never
// re-orders or annotates. A surviving change is bit-identical to its input (same struct);
// the filter asserts nothing about cause, only "lies earlier in time". This guards the
// charter line "deterministic filter over MEASURED timestamps, NOT an inference of cause".
func TestAntecedentsArrowIsPureFilterNotCausal(t *testing.T) {
	changes := Resolve(ParseEvents(fixtureLines(t)), testResolver)
	got := Antecedents(changes, onset, lookback)
	// Every survivor must be present in (and unmodified from) the input set, and the
	// output must preserve the input's chronological order.
	byID := map[string]ChangeEvent{}
	for _, c := range changes {
		byID[c.AuditID] = c
	}
	var lastTs time.Time
	for i, c := range got {
		orig, ok := byID[c.AuditID]
		if !ok {
			t.Fatalf("Antecedents invented a change not in the input: %+v", c)
		}
		if !reflect.DeepEqual(orig, c) {
			t.Errorf("Antecedents mutated a survivor (it must be a pure filter): %+v vs %+v", c, orig)
		}
		if i > 0 && c.Timestamp.Before(lastTs) {
			t.Error("Antecedents output is not in chronological order")
		}
		lastTs = c.Timestamp
	}
}

// TestAntecedentsZeroOnsetGuardHasTeeth pins the `onset.IsZero()` half of the
// Antecedents guard with a SHARP input the window arithmetic alone CANNOT exclude.
//
// A zero onset is the Go zero time (0001-01-01T00:00:00Z). On the FIXTURE (all
// timestamps in 2026, i.e. AFTER the zero time) the window `[zero-lookback, zero)`
// is empty by arithmetic alone — so a fixture-based assertion is VACUOUS: it passes
// whether or not the guard is present. To give the guard teeth we feed a change whose
// SOURCE timestamp lies in 0000-12-31 — strictly BEFORE the zero time and inside the
// would-be window `[zero-lookback, zero)`. Without the `onset.IsZero()` guard that
// change SURVIVES the arithmetic (proven below), so the guard is the ONLY thing that
// excludes it. A regression that dropped the guard and treated the zero onset as a
// real window bound would surface this antecedent against a non-existent "incident at
// the epoch" — exactly the silently-widened window the guard exists to forbid.
func TestAntecedentsZeroOnsetGuardHasTeeth(t *testing.T) {
	zero := time.Time{}
	lb := 10 * time.Minute
	// A change 5m before the zero time: itself non-zero, and inside [zero-lb, zero).
	preZero := ChangeEvent{AuditID: "pre-zero", Timestamp: zero.Add(-5 * time.Minute)}
	if preZero.Timestamp.IsZero() {
		t.Fatal("test setup: the probe timestamp must be non-zero")
	}
	// Prove the window ARITHMETIC alone would admit this change (so the assertion below
	// distinguishes the guard from the arithmetic — it is not vacuous).
	lo := zero.Add(-lb)
	if !(preZero.Timestamp.Before(zero) && !preZero.Timestamp.Before(lo)) {
		t.Fatalf("test setup: probe ts %v must fall inside the would-be window [%v, %v)",
			preZero.Timestamp, lo, zero)
	}
	if got := Antecedents([]ChangeEvent{preZero}, zero, lb); got != nil {
		t.Errorf("zero onset must yield NO antecedents even for a pre-epoch change "+
			"(the onset.IsZero guard, not the window arithmetic, must exclude it); got %d: %+v",
			len(got), got)
	}
}

// TestAntecedentsNonPositiveLookback asserts the HONEST true property: a non-positive
// lookback yields no antecedents (we never invent a window).
//
// Honesty note on teeth: for lookback <= 0 the guard is NOT observable through this
// public API, because the window arithmetic ALREADY excludes everything —
//   - lookback == 0  => lo == onset, so the filter is `ts < onset && ts >= onset`
//     (X && !X), unsatisfiable for every input;
//   - lookback  < 0  => lo == onset + |lookback| > onset, so `ts < onset && ts >= lo`
//     is unsatisfiable (no ts is both before onset and at/after a bound past onset).
//
// There is no input — not even a pathological pre-epoch one — that the `lookback <= 0`
// guard excludes but the arithmetic admits, so this case cannot prove the guard's teeth
// the way the zero-onset case can. It is asserted as a defensive belt-and-suspenders
// invariant, and this docstring states plainly that it does NOT distinguish the guard
// from the window arithmetic (unlike TestAntecedentsZeroOnsetGuardHasTeeth, which does).
func TestAntecedentsNonPositiveLookback(t *testing.T) {
	changes := Resolve(ParseEvents(fixtureLines(t)), testResolver)
	if len(changes) == 0 {
		t.Fatal("fixture parsed to zero changes; the degenerate-input check would be vacuous")
	}
	if got := Antecedents(changes, onset, 0); got != nil {
		t.Errorf("lookback=0 must yield no antecedents, got %d", len(got))
	}
	if got := Antecedents(changes, onset, -time.Minute); got != nil {
		t.Errorf("negative lookback must yield no antecedents, got %d", len(got))
	}
}

// TestHypothesizeDeltaMatchesArrow: for every staged hypothesis, deltaSeconds is the
// POSITIVE gap from change to onset and the change's RFC3339 timestamp is strictly before
// the incident onset. The output frames temporal adjacency, never causation: the relation
// is direction-free and the evidence labels say "arrow-of-time", not "caused".
func TestHypothesizeDeltaMatchesArrow(t *testing.T) {
	changes := Resolve(ParseEvents(fixtureLines(t)), testResolver)
	inc := sampleIncident()
	for _, h := range Hypothesize(changes, inc, lookback) {
		d, ok := h.Payload["deltaSeconds"].(int64)
		if !ok || d <= 0 {
			t.Fatalf("deltaSeconds must be a positive int64 (change precedes onset), got %v", h.Payload["deltaSeconds"])
		}
		changeTs, err := time.Parse(time.RFC3339Nano, h.Payload["changeTs"].(string))
		if err != nil {
			t.Fatalf("changeTs not RFC3339Nano: %v", err)
		}
		if !changeTs.Before(inc.Onset) {
			t.Errorf("ARROW-OF-TIME BREACH: staged change ts %v is not before onset %v", changeTs, inc.Onset)
		}
		// deltaSeconds must equal the truncated (onset - changeTs) gap.
		wantDelta := int64(inc.Onset.Sub(changeTs) / time.Second)
		if d != wantDelta {
			t.Errorf("deltaSeconds=%d, want %d (onset - changeTs)", d, wantDelta)
		}
	}
}

// ---- The co-occurrence is NEVER a cause: charter discipline on the OUTPUT text ----

// forbiddenCausalSubstrings are words the audit lane's output must never contain: a
// temporal+spatial adjacency is a co-occurrence (the ice-cream/drownings discipline),
// surfaced as a hypothesis, never a cause (doc.go: "The words cause/caused/root-cause
// appear nowhere in the output.").
var forbiddenCausalSubstrings = []string{"cause", "caused", "causes", "because", "root-cause", "root cause", "due to", "led to", "resulted in", "triggered by"}

// TestOutputNeverClaimsCausation scans every rendered string in every staged candidate —
// relation, subject, payload values, evidence detail/ref/kind — for a causal claim. The
// only permitted relation is the direction-free "observed-adjacency". This is the cardinal
// charter gate: a co-occurrence may be PRESENTED adjacently, never FUSED into a cause.
func TestOutputNeverClaimsCausation(t *testing.T) {
	changes := Resolve(ParseEvents(fixtureLines(t)), testResolver)
	inc := sampleIncident()
	hyps := Hypothesize(changes, inc, lookback)
	if len(hyps) == 0 {
		t.Fatal("no hypotheses produced; the charter scan would be vacuous")
	}
	scan := func(where, s string) {
		low := strings.ToLower(s)
		for _, bad := range forbiddenCausalSubstrings {
			if strings.Contains(low, bad) {
				t.Errorf("CHARTER BREACH: causal word %q surfaced in %s: %q", bad, where, s)
			}
		}
	}
	for _, h := range hyps {
		if h.Kind != candidate.KindCausalHypothesis {
			t.Fatalf("CHARTER BREACH: audit emitted kind %q (never an authoritative/causal edge)", h.Kind)
		}
		if h.Relation != "observed-adjacency" {
			t.Fatalf("relation must be the direction-free observed-adjacency, got %q", h.Relation)
		}
		// The store's own guard must accept it (a causal edge would be rejected).
		if err := candidate.Validate(h); err != nil {
			t.Errorf("candidate rejected by the structural guard: %v", err)
		}
		scan("relation", h.Relation)
		scan("subject", h.Subject)
		scan("reason", h.Reason)
		scan("lineage.method", h.Lineage.Method)
		for k, v := range h.Payload {
			if s, ok := v.(string); ok {
				scan("payload."+k, s)
			}
		}
		for _, e := range h.Evidence {
			scan("evidence.kind", e.Kind)
			scan("evidence.ref", e.Ref)
			scan("evidence.detail", e.Detail)
		}
	}
}

// TestHypothesizeDifferentNamespaceNotJoined: a change in a different namespace with no
// role-CEI match is NEVER staged — adjacency in time without co-location is not surfaced
// as a hypothesis. (The coredns patch in kube-system is antecedent in time but in another
// namespace.) A regression that joined on time alone would light this up.
func TestHypothesizeDifferentNamespaceNotJoined(t *testing.T) {
	changes := Resolve(ParseEvents(fixtureLines(t)), testResolver)
	inc := sampleIncident()
	for _, h := range Hypothesize(changes, inc, lookback) {
		if strings.Contains(h.Subject, "a-coredns-patch-otherns") {
			t.Errorf("a different-namespace, non-CEI change must NOT be staged (time alone is not a join): %+v", h)
		}
	}
}

// ---- Honest partial coverage ----

// TestHonestUnresolvedRoleNeverGuessed: an object the resolver has not seen keeps its OWN
// coordinate key and is flagged RoleUnresolved — never a guessed role, and (since its
// own-key never equals the incident role CEI) never an exact-CEI join. This is the
// "honest partial coverage" contract from doc.go: a binding gap is stated, never hidden.
func TestHonestUnresolvedRoleNeverGuessed(t *testing.T) {
	changes := Resolve(ParseEvents(fixtureLines(t)), testResolver)
	sawUnresolved := false
	for _, c := range changes {
		if c.AuditID == "a-deploy-patch" {
			continue // the only resolved one
		}
		sawUnresolved = true
		if !c.RoleUnresolved {
			t.Errorf("%s must be flagged RoleUnresolved (the store has not seen it)", c.AuditID)
		}
		if !strings.HasPrefix(c.RoleCEI, "audit:") {
			t.Errorf("%s unresolved role must be its OWN coordinate key (audit:...), got %q — never a guessed role", c.AuditID, c.RoleCEI)
		}
		if c.RoleCEI == incidentRole {
			t.Errorf("%s own-key collided with the incident role CEI; an unresolved change must never form an exact-CEI join", c.AuditID)
		}
	}
	if !sawUnresolved {
		t.Fatal("fixture had no unresolved change; the honest-coverage check would be vacuous")
	}
	// An unresolved change can still co-locate by the WEAKER namespace tier, surfaced
	// explicitly as joinTier="namespace" (never silently upgraded to role-cei).
	inc := sampleIncident()
	for _, h := range Hypothesize(changes, inc, lookback) {
		if strings.Contains(h.Subject, "a-cm-update") { // unresolved, same namespace
			if h.Payload["joinTier"] != string(JoinNamespace) {
				t.Errorf("an unresolved same-namespace change must join at the WEAKER namespace tier, got %v", h.Payload["joinTier"])
			}
		}
	}
}

// TestLookbackWindowCapDropsOldChanges is the package-level analogue of the runtime
// tail-cap: changes older than the stated lookback are HONESTLY dropped (not silently
// retained), and the cutoff is the explicit window bound, not a guess. Narrowing the
// window must shrink the antecedent set monotonically — coverage is bounded and stated.
func TestLookbackWindowCapDropsOldChanges(t *testing.T) {
	changes := Resolve(ParseEvents(fixtureLines(t)), testResolver)
	// Fixture deltas from onset (s): secret=300, deploy=120, cm=60, coredns=30; after=-30.
	wide := Antecedents(changes, onset, 10*time.Minute)   // 600s window: 4 antecedents
	narrow := Antecedents(changes, onset, 90*time.Second) // 90s window: drops secret(300) + deploy(120)
	if len(wide) <= len(narrow) {
		t.Fatalf("a narrower lookback must drop older changes: wide=%d narrow=%d", len(wide), len(narrow))
	}
	// Every change kept by the narrow window must lie strictly inside it (the cap is exact).
	lo := onset.Add(-90 * time.Second)
	for _, c := range narrow {
		if c.Timestamp.Before(lo) || !c.Timestamp.Before(onset) {
			t.Errorf("a change outside the stated [onset-lookback, onset) window leaked past the cap: %+v", c)
		}
	}
	// And the dropped-but-recent oldest change (secret @ -300s) is absent from the narrow set.
	for _, c := range narrow {
		if c.AuditID == "a-secret-update" {
			t.Error("a change older than the narrow lookback must be dropped by the window cap")
		}
	}
}
