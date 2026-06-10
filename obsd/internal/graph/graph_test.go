package graph

import (
	"testing"
)

const kgPath = "../../../ontology/graph/k8s_signal_kg.json"

func loadKG(t *testing.T) *Graph {
	t.Helper()
	g, err := Load(kgPath)
	if err != nil {
		t.Fatalf("Load(%s): %v", kgPath, err)
	}
	return g
}

// The loader's counts must match the graph's own self-reported stats AND the
// blueprint's reference scale (doc 02 §2: 842 nodes, 3759 edges, 589 signals).
func TestLoadRealKGCounts(t *testing.T) {
	g := loadKG(t)

	if got := g.NodeCount(); got != 842 {
		t.Errorf("NodeCount = %d, want 842", got)
	}
	if got := len(g.Edges); got != 3759 {
		t.Errorf("edges = %d, want 3759", got)
	}

	typeCounts := map[string]int{
		"Signal":            589,
		"CorrelationGroup":  38,
		"EquivalenceGroup":  35,
		"Entity":            47,
		"Tool":              38,
		"CapabilityPrereq":  42,
		"DistroVersionGate": 19,
		"Gotcha":            21,
		"Agent":             6,
		"Modality":          7,
	}
	got := map[string]int{
		"Signal":            len(g.Signals),
		"CorrelationGroup":  len(g.Phenomena),
		"EquivalenceGroup":  len(g.EquivalenceGroups),
		"Entity":            len(g.Entities),
		"Tool":              len(g.Tools),
		"CapabilityPrereq":  len(g.Capabilities),
		"DistroVersionGate": len(g.DistroGates),
		"Gotcha":            len(g.Gotchas),
		"Agent":             len(g.Agents),
		"Modality":          len(g.Modalities),
	}
	for typ, want := range typeCounts {
		if got[typ] != want {
			t.Errorf("%s count = %d, want %d", typ, got[typ], want)
		}
	}

	// 457 of the 589 signals are Metric-modality numeric series (doc 02 §2).
	if m := len(g.MetricSignals()); m != 457 {
		t.Errorf("metric signals = %d, want 457", m)
	}
}

func TestEdgeIndexAndTypes(t *testing.T) {
	g := loadKG(t)
	want := map[string]int{
		"attaches_to":            589,
		"emitted_by":             817,
		"has_modality":           589,
		"requires_capability":    675,
		"participates_in":        180,
		"phenomenon_relation":    12,
		"in_equivalence_group":   66,
		"has_gotcha":             472,
		"derived_from":           142,
		"owned_by_agent":         60,
		"cross_reference":        57,
		"behaves_differently_in": 100,
	}
	for typ, n := range want {
		if got := len(g.EdgesByType(typ)); got != n {
			t.Errorf("EdgesByType(%q) = %d, want %d", typ, got, n)
		}
	}

	// The src/dst indexes must be consistent with the edge list.
	total := 0
	for _, es := range g.edgesBySrc {
		total += len(es)
	}
	if total != len(g.Edges) {
		t.Errorf("edgesBySrc total = %d, want %d", total, len(g.Edges))
	}
}

// Phenomenon membership resolves from participates_in edges, and relations from
// phenomenon_relation edges; their totals match the edges that point at a known
// phenomenon (no dangling resolution).
func TestPhenomenonResolution(t *testing.T) {
	g := loadKG(t)

	var members, relations int
	for _, p := range g.Phenomena {
		members += len(p.Members)
		relations += len(p.Relations)
	}

	wantMembers := 0
	for _, e := range g.EdgesByType("participates_in") {
		if g.Phenomena[e.Dst] != nil {
			wantMembers++
		}
	}
	if members != wantMembers {
		t.Errorf("resolved members = %d, want %d (participates_in into known phenomena)", members, wantMembers)
	}
	if members == 0 {
		t.Error("no phenomenon members resolved")
	}

	wantRel := 0
	for _, e := range g.EdgesByType("phenomenon_relation") {
		if g.Phenomena[e.Src] != nil {
			wantRel++
		}
	}
	if relations != wantRel {
		t.Errorf("resolved relations = %d, want %d", relations, wantRel)
	}
}

// The OOM phenomenon carries authored inline members (temporal-tagged) but — per the
// doc 14 A14 gap — NO declared span.
func TestOOMPhenomenonShape(t *testing.T) {
	g := loadKG(t)
	p, ok := g.Phenomena["PHEN_OOM_KILL_CGROUP"]
	if !ok {
		t.Fatal("expected PHEN_OOM_KILL_CGROUP")
	}
	if p.Label == "" {
		t.Error("phenomenon missing label")
	}
	if len(p.InlineMembers) != len(p.RawSignals) || len(p.InlineMembers) == 0 {
		t.Errorf("inline members = %d, want %d (from signals[])", len(p.InlineMembers), len(p.RawSignals))
	}
	if p.HasSpan() {
		t.Error("expected NO declared span (the doc 14 A14 authoring gap)")
	}
	// Inline member tuples are [pattern, role, temporal_tag, note].
	for _, m := range p.InlineMembers {
		if m.Pattern == "" || m.TemporalTag == "" {
			t.Errorf("malformed inline member: %+v", m)
		}
	}
}

func TestContentHashDeterministicAndPinned(t *testing.T) {
	g := loadKG(t)
	if len(g.Version) < 8 || g.Version[:7] != "sha256:" {
		t.Errorf("version %q is not a sha256 pin", g.Version)
	}
	g2 := loadKG(t)
	if g.Version != g2.Version {
		t.Errorf("content hash not deterministic: %q vs %q", g.Version, g2.Version)
	}
}

// Parse cross-checks the release's declared stats against the parsed counts, so a
// truncated release (stats say N, body has fewer) is rejected at ingestion.
func TestParseRejectsStatsMismatch(t *testing.T) {
	raw := []byte(`{"stats":{"nodes_total":99},"nodes":[{"id":"X","type":"Entity"}],"edges":[]}`)
	if _, err := Parse(raw); err == nil {
		t.Error("Parse must reject a stats/parsed-count mismatch (truncated release)")
	}
}

func TestParseAcceptsMatchingStats(t *testing.T) {
	raw := []byte(`{"stats":{"nodes_total":1,"edges_total":0,"signals":0},"nodes":[{"id":"X","type":"Entity"}],"edges":[]}`)
	if _, err := Parse(raw); err != nil {
		t.Errorf("Parse must accept matching stats: %v", err)
	}
}

// The source-spreadsheet provenance (sheet) is captured on every signal (no silent loss).
func TestSignalSheetCaptured(t *testing.T) {
	g := loadKG(t)
	withSheet := 0
	for _, s := range g.Signals {
		if s.Sheet != "" {
			withSheet++
		}
	}
	if withSheet != len(g.Signals) {
		t.Errorf("sheet captured on %d/%d signals, want all", withSheet, len(g.Signals))
	}
}

func TestNodeTypeLookup(t *testing.T) {
	g := loadKG(t)
	if typ, ok := g.NodeType("PHEN_OOM_KILL_CGROUP"); !ok || typ != "CorrelationGroup" {
		t.Errorf("NodeType(phenomenon) = (%q,%v), want CorrelationGroup,true", typ, ok)
	}
	if _, ok := g.NodeType("does-not-exist"); ok {
		t.Error("NodeType should miss on unknown id")
	}
	// Equivalence groups carry regex patterns (the dialect bridge).
	if eg := g.EquivalenceGroups["EQG_WORKING_SET"]; eg == nil || len(eg.Patterns) == 0 {
		t.Error("expected EQG_WORKING_SET with patterns")
	}
}
