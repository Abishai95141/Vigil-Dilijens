package flow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// The transitive-chain gate corpus (doc 15 cap. B): a STANDALONE deterministic gate that
// folds synthetic-but-real-shaped flow topologies + MEASURED-degraded sets through the
// REAL TransitiveChains path, graded against a LABEL ORACLE fixed by construction. The
// anti-shallow core is the CARDINAL rule: independently-coincident faults (not flow-
// connected) produce ZERO chains. TransitiveChains is a pure function of its inputs, so
// the frozen corpus reproducing byte-identically IS the off-digest determinism proof.
//
// Regenerate: REGEN_TRANSITIVE_CORPUS=1 go test ./obsd/internal/flow -run RegenTransitiveCorpus

const transitiveCorpusDir = "../../../corpus/transitive-chain"

var transAt = time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

type tEdge struct{ caller, callee string } // "ns/name" -> "ns/name", a flow (call) edge
type tDeg struct{ wl, phen string }        // "ns/name", its measured phenomenon

// tLabel is the per-scenario ground-truth oracle the python scorer grades against.
type tLabel struct {
	Bundle           string     `json:"bundle"`
	Scenario         string     `json:"scenario"`
	ExpectChains     int        `json:"expectChains"`
	ExpectRoots      []string   `json:"expectRoots"`      // the structural root(s), sorted
	ExpectPath       [][]string `json:"expectPath"`       // [[upstream,downstream],...] union over chains, sorted
	IndependentPairs [][]string `json:"independentPairs"` // pairs that must NEVER co-occur in one chain (either order)
	Note             string     `json:"note"`
}

type tScenario struct {
	lbl     tLabel
	edges   []tEdge
	degs    []tDeg
	maxHops int
}

func splitWL(s string) (ns, name string) {
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return "", s
}

func transScenarios() []tScenario {
	return []tScenario{
		{
			lbl: tLabel{Scenario: "linear-chain", ExpectChains: 1, ExpectRoots: []string{"traffic/inference"},
				ExpectPath: [][]string{{"traffic/inference", "traffic/aggregation"}, {"traffic/aggregation", "traffic/db"}, {"traffic/db", "traffic/prediction"}},
				Note:       "the smart-traffic chain: inference(mem)->aggregation(queue)->db(latency)->prediction(stale), all degraded, flow-connected. ONE ordered root-cause chain."},
			edges: []tEdge{{"traffic/aggregation", "traffic/inference"}, {"traffic/db", "traffic/aggregation"}, {"traffic/prediction", "traffic/db"}},
			degs:  []tDeg{{"traffic/inference", "PHEN_CONTAINER_MEM_PRESSURE"}, {"traffic/aggregation", "PHEN_APP_QUEUE_SATURATION"}, {"traffic/db", "PHEN_APP_LATENCY"}, {"traffic/prediction", "PHEN_APP_DATA_STALENESS"}},
		},
		{
			lbl: tLabel{Scenario: "independent-faults", ExpectChains: 0,
				IndependentPairs: [][]string{{"a/lonely1", "b/lonely2"}},
				Note:             "two coincident faults in DISJOINT flow components — the CARDINAL rule: ZERO chains, never one merged story."},
			edges: []tEdge{{"a/caller1", "a/lonely1"}, {"b/caller2", "b/lonely2"}},
			degs:  []tDeg{{"a/lonely1", "PHEN_OOM_KILL_CGROUP"}, {"b/lonely2", "PHEN_APP_DATA_STALENESS"}},
		},
		{
			lbl: tLabel{Scenario: "silent-intermediate", ExpectChains: 0,
				IndependentPairs: [][]string{{"traffic/inference", "traffic/prediction"}},
				Note:             "inference(deg) <- aggregation(SILENT) <- prediction(deg): the chain is NEVER bridged across the unmeasured aggregation (weakest-input). Zero asserted chains; the silence is a stated gap."},
			edges: []tEdge{{"traffic/aggregation", "traffic/inference"}, {"traffic/prediction", "traffic/aggregation"}},
			degs:  []tDeg{{"traffic/inference", "PHEN_CONTAINER_MEM_PRESSURE"}, {"traffic/prediction", "PHEN_APP_DATA_STALENESS"}},
		},
		{
			lbl: tLabel{Scenario: "fan-in", ExpectChains: 1, ExpectRoots: []string{"t/shared"},
				ExpectPath: [][]string{{"t/shared", "t/callerA"}, {"t/shared", "t/callerB"}},
				Note:       "one degraded callee impacts TWO degraded callers — two hop-1 steps, one chain, no merge."},
			edges: []tEdge{{"t/callerA", "t/shared"}, {"t/callerB", "t/shared"}},
			degs:  []tDeg{{"t/shared", "PHEN_OOM_KILL_CGROUP"}, {"t/callerA", "PHEN_APP_QUEUE_SATURATION"}, {"t/callerB", "PHEN_APP_QUEUE_SATURATION"}},
		},
		{
			lbl: tLabel{Scenario: "two-disjoint-real-chains", ExpectChains: 2,
				ExpectRoots:      []string{"x/x1", "y/y1"},
				ExpectPath:       [][]string{{"x/x1", "x/x2"}, {"y/y1", "y/y2"}},
				IndependentPairs: [][]string{{"x/x1", "y/y1"}, {"x/x1", "y/y2"}, {"x/x2", "y/y1"}, {"x/x2", "y/y2"}},
				Note:             "TWO genuine but UNRELATED 2-node chains (different namespaces, disjoint flow). Each is its own chain; the two are NEVER linked into one."},
			edges: []tEdge{{"x/x2", "x/x1"}, {"y/y2", "y/y1"}},
			degs:  []tDeg{{"x/x1", "P"}, {"x/x2", "P"}, {"y/y1", "P"}, {"y/y2", "P"}},
		},
		{
			lbl:   tLabel{Scenario: "healthy-no-degradation", ExpectChains: 0, Note: "a flow topology with NO degraded node — no chain, never invented."},
			edges: []tEdge{{"t/a", "t/b"}, {"t/c", "t/b"}},
			degs:  nil,
		},
	}
}

func foldTransitiveScenario(t *testing.T, sc tScenario) []Chain {
	t.Helper()
	edges := identity.NewEdgeStore(func() time.Time { return transAt },
		map[identity.EdgeType]time.Duration{EdgeTypeFlow: flowBudget}, time.Hour)
	for _, e := range sc.edges {
		cns, cname := splitWL(e.caller)
		dns, dname := splitWL(e.callee)
		edges.Assert(EdgeTypeFlow, roleCEId(cns, cname), roleCEId(dns, dname), transAt)
	}
	var degraded []DegradedWorkload
	for _, d := range sc.degs {
		ns, name := splitWL(d.wl)
		degraded = append(degraded, DegradedWorkload{
			CEI: roleCEId(ns, name), Label: d.wl, Phenomenon: d.phen, Detail: d.phen + " finding (degraded)",
		})
	}
	w := identity.TimeWindow{Start: transAt, End: transAt}
	return TransitiveChains(edges, degraded, transRel(), w, transAt, sc.maxHops)
}

func marshalChains(chains []Chain) []byte {
	var buf []byte
	for i := range chains {
		b, _ := json.Marshal(chains[i])
		buf = append(buf, b...)
		buf = append(buf, '\n')
	}
	return buf
}

func TestRegenTransitiveCorpus(t *testing.T) {
	if os.Getenv("REGEN_TRANSITIVE_CORPUS") != "1" {
		t.Skip("set REGEN_TRANSITIVE_CORPUS=1 to regenerate corpus/transitive-chain")
	}
	if err := os.MkdirAll(transitiveCorpusDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, sc := range transScenarios() {
		chains := foldTransitiveScenario(t, sc)
		if err := os.WriteFile(filepath.Join(transitiveCorpusDir, "chains-"+sc.lbl.Scenario+".jsonl"), marshalChains(chains), 0o644); err != nil {
			t.Fatal(err)
		}
		lbl := sc.lbl
		lbl.Bundle = "transitive-" + lbl.Scenario
		sort.Strings(lbl.ExpectRoots)
		sortPairs(lbl.ExpectPath)
		sortPairs(lbl.IndependentPairs)
		b, _ := json.MarshalIndent(lbl, "", "  ")
		if err := os.WriteFile(filepath.Join(transitiveCorpusDir, "label-"+sc.lbl.Scenario+".json"), append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("regenerated %d transitive-chain scenarios into %s", len(transScenarios()), transitiveCorpusDir)
}

// TestTransitiveCorpusFrozenConsistent is the always-on determinism guard: re-running the
// REAL TransitiveChains over the scenarios reproduces the FROZEN corpus byte-for-byte
// (the off-digest "pure function" determinism claim, doc 15 cap. B §3).
func TestTransitiveCorpusFrozenConsistent(t *testing.T) {
	for _, sc := range transScenarios() {
		got := marshalChains(foldTransitiveScenario(t, sc))
		want, err := os.ReadFile(filepath.Join(transitiveCorpusDir, "chains-"+sc.lbl.Scenario+".jsonl"))
		if err != nil {
			t.Fatalf("frozen corpus missing for %s: %v (REGEN to create)", sc.lbl.Scenario, err)
		}
		if string(got) != string(want) {
			t.Errorf("transitive-chain corpus drift for %s — re-run differs from frozen (REGEN to update intentionally)", sc.lbl.Scenario)
		}
	}
}

func sortPairs(p [][]string) {
	sort.Slice(p, func(i, j int) bool {
		if p[i][0] != p[j][0] {
			return p[i][0] < p[j][0]
		}
		return p[i][1] < p[j][1]
	})
}
