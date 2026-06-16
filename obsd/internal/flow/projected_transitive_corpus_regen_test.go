package flow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// The projected-transitive (cap. D) gate corpus: a STANDALONE deterministic gate that
// folds synthetic flow topologies + FORECAST roots through the REAL ProjectedTransitiveChains
// path, graded against a LABEL ORACLE. The CARDINAL floor is BAND-MONOTONICITY: across every
// chain, no inherited band ever NARROWS downstream (a downstream node tighter than its parent
// is PROJECTED-dressed-as-stronger — an absolute-zero failure, doc 15 §4.D). The forecast is
// PROJECTED by class, so the gate certifies the JOIN, not the tick-digest — but the producer
// is a pure function, so the frozen corpus still reproduces byte-identically (the drift guard).
//
// Regenerate: REGEN_PROJTRANS_CORPUS=1 go test ./obsd/internal/flow -run RegenProjTransCorpus

const projTransCorpusDir = "../../../corpus/projected-transitive"

type pScenario struct {
	lbl     tLabel
	edges   []tEdge
	roots   []pRoot
	now     time.Time // zero ⇒ transAt (mid-day); set to anchor a scenario near a boundary
	maxHops int       // 0 ⇒ unbounded
}

type pRoot struct {
	wl            string
	metric        string
	loMin, hiMin  int
	beyondHorizon bool
}

func projTransScenarios() []pScenario {
	return []pScenario{
		{
			lbl: tLabel{Scenario: "linear-2hop", ExpectChains: 1, ExpectRoots: []string{"traffic/back"},
				ExpectPath: [][]string{{"traffic/back", "traffic/mid"}, {"traffic/mid", "traffic/front"}},
				Note:       "one forecast root (back, working_set band +30..+50) ripples to mid then front; the band WIDENS per hop."},
			edges: []tEdge{{"traffic/mid", "traffic/back"}, {"traffic/front", "traffic/mid"}},
			roots: []pRoot{{wl: "traffic/back", metric: "working_set", loMin: 30, hiMin: 50}},
		},
		{
			lbl: tLabel{Scenario: "fan-out", ExpectChains: 1, ExpectRoots: []string{"t/shared"},
				ExpectPath: [][]string{{"t/shared", "t/callerA"}, {"t/shared", "t/callerB"}},
				Note:       "one forecast root impacts TWO direct callers — both hop-1 bands widen equally vs the root."},
			edges: []tEdge{{"t/callerA", "t/shared"}, {"t/callerB", "t/shared"}},
			roots: []pRoot{{wl: "t/shared", metric: "working_set", loMin: 30, hiMin: 50}},
		},
		{
			lbl: tLabel{Scenario: "two-roots", ExpectChains: 2, ExpectRoots: []string{"a/ra", "b/rb"},
				ExpectPath:       [][]string{{"a/ra", "a/ca"}, {"b/rb", "b/cb"}},
				IndependentPairs: [][]string{{"a/ra", "b/rb"}, {"a/ca", "b/cb"}},
				Note:             "TWO independently-warned roots → TWO chains (one forecast root per chain), never merged."},
			edges: []tEdge{{"a/ca", "a/ra"}, {"b/cb", "b/rb"}},
			roots: []pRoot{{wl: "a/ra", metric: "m", loMin: 10, hiMin: 20}, {wl: "b/rb", metric: "m", loMin: 10, hiMin: 20}},
		},
		{
			lbl: tLabel{Scenario: "open-horizon", ExpectChains: 1, ExpectRoots: []string{"t/root"},
				ExpectPath: [][]string{{"t/root", "t/caller"}},
				Note:       "a root whose far edge is open (crossing may exceed the horizon) propagates an OPEN band — never a collapsed line."},
			edges: []tEdge{{"t/caller", "t/root"}},
			roots: []pRoot{{wl: "t/root", metric: "m", loMin: 30, hiMin: 50, beyondHorizon: true}},
		},
		{
			lbl:   tLabel{Scenario: "no-caller", ExpectChains: 0, Note: "a lone forecast root with no caller is a warning, not a cascade — no chain."},
			edges: nil,
			roots: []pRoot{{wl: "t/lonely", metric: "m", loMin: 10, hiMin: 20}},
		},
		{
			lbl:   tLabel{Scenario: "no-forecast", ExpectChains: 0, Note: "flow edges but NO forecast root — no projected cascade, never invented."},
			edges: []tEdge{{"t/a", "t/b"}},
			roots: nil,
		},
		{
			// now = 23:40Z so the root band [+8,+14] = [23:48Z,23:54Z] widens PAST midnight; the
			// gate's band-monotonicity floor must judge it on the RFC3339 edges, not HH:MMZ.
			lbl: tLabel{Scenario: "midnight-wrap", ExpectChains: 1, ExpectRoots: []string{"t/back"},
				ExpectPath: [][]string{{"t/back", "t/mid"}, {"t/mid", "t/front"}},
				Note:       "a forecast band straddling 00:00Z UTC still widens every hop — no midnight-wrap drop."},
			edges: []tEdge{{"t/mid", "t/back"}, {"t/front", "t/mid"}},
			roots: []pRoot{{wl: "t/back", metric: "working_set", loMin: 8, hiMin: 14}},
			now:   time.Date(2026, 6, 16, 23, 40, 0, 0, time.UTC),
		},
		{
			lbl: tLabel{Scenario: "tight-root", ExpectChains: 1, ExpectRoots: []string{"t/root"},
				ExpectPath: [][]string{{"t/root", "t/caller"}},
				Note:       "a ZERO-WIDTH (over-confident) forecast root never collapses the band to a line — the far edge is OPEN (doc 01)."},
			edges: []tEdge{{"t/caller", "t/root"}},
			roots: []pRoot{{wl: "t/root", metric: "m", loMin: 30, hiMin: 30}}, // EarliestAt == LatestAt
		},
		{
			lbl: tLabel{Scenario: "maxhops-bounded", ExpectChains: 1, ExpectRoots: []string{"t/n1"},
				ExpectPath: [][]string{{"t/n1", "t/n2"}, {"t/n2", "t/n3"}},
				Note:       "a 3-deep chain bounded at maxHops=2 → 2 steps + a stated hop-ceiling gap (the bounded config the operator runs)."},
			edges:   []tEdge{{"t/n2", "t/n1"}, {"t/n3", "t/n2"}, {"t/n4", "t/n3"}},
			roots:   []pRoot{{wl: "t/n1", metric: "m", loMin: 30, hiMin: 50}},
			maxHops: 2,
		},
	}
}

func foldProjTransScenario(sc pScenario) []Chain {
	now := sc.now
	if now.IsZero() {
		now = transAt
	}
	edges := identity.NewEdgeStore(func() time.Time { return now },
		map[identity.EdgeType]time.Duration{EdgeTypeFlow: flowBudget}, time.Hour)
	for _, e := range sc.edges {
		cns, cname := splitWL(e.caller)
		dns, dname := splitWL(e.callee)
		edges.Assert(EdgeTypeFlow, roleCEId(cns, cname), roleCEId(dns, dname), now)
	}
	var roots []ProjectedDegradedWorkload
	for _, r := range sc.roots {
		ns, name := splitWL(r.wl)
		d := projRoot(ns, name, r.metric, now, r.loMin, r.hiMin)
		d.LatestBeyondHorizon = r.beyondHorizon
		roots = append(roots, d)
	}
	w := identity.TimeWindow{Start: now, End: now}
	return ProjectedTransitiveChains(edges, roots, transRel(), w, now, sc.maxHops)
}

func TestRegenProjTransCorpus(t *testing.T) {
	if os.Getenv("REGEN_PROJTRANS_CORPUS") != "1" {
		t.Skip("set REGEN_PROJTRANS_CORPUS=1 to regenerate corpus/projected-transitive")
	}
	if err := os.MkdirAll(projTransCorpusDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, sc := range projTransScenarios() {
		chains := foldProjTransScenario(sc)
		if err := os.WriteFile(filepath.Join(projTransCorpusDir, "chains-"+sc.lbl.Scenario+".jsonl"), marshalChains(chains), 0o644); err != nil {
			t.Fatal(err)
		}
		lbl := sc.lbl
		lbl.Bundle = "projtrans-" + lbl.Scenario
		sortPairs(lbl.ExpectPath)
		sortPairs(lbl.IndependentPairs)
		b, _ := json.MarshalIndent(lbl, "", "  ")
		if err := os.WriteFile(filepath.Join(projTransCorpusDir, "label-"+sc.lbl.Scenario+".json"), append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("regenerated %d projected-transitive scenarios into %s", len(projTransScenarios()), projTransCorpusDir)
}

// TestProjTransCorpusFrozenConsistent is the always-on determinism guard.
func TestProjTransCorpusFrozenConsistent(t *testing.T) {
	for _, sc := range projTransScenarios() {
		got := marshalChains(foldProjTransScenario(sc))
		want, err := os.ReadFile(filepath.Join(projTransCorpusDir, "chains-"+sc.lbl.Scenario+".jsonl"))
		if err != nil {
			t.Fatalf("frozen corpus missing for %s: %v (REGEN to create)", sc.lbl.Scenario, err)
		}
		if string(got) != string(want) {
			t.Errorf("projected-transitive corpus drift for %s — re-run differs from frozen (REGEN to update)", sc.lbl.Scenario)
		}
	}
}
