package api

import (
	"sort"
	"strings"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
)

// TopologyView is the topology surface payload (doc 10 §3.2, M3): the bound
// customer graph rendered live — entities as nodes, valid edges as links
// (suspect edges visibly distinct), with the CURRENT-condition marks overlaid
// (matched phenomena, loud entities). Predictive marks are a SEPARATE visual
// language added in M5/Phase 2 — there are none here, by construction, so "is"
// and "might" cannot be confused. The TS type mirrors this shape.
type TopologyView struct {
	ClusterID    string          `json:"clusterId"`
	GraphVersion string          `json:"graphVersion"`
	GeneratedAt  time.Time       `json:"generatedAt"`
	Nodes        []TopoNode      `json:"nodes"`
	Edges        []TopoEdge      `json:"edges"`
	Summary      TopologySummary `json:"summary"`
	Truncated    int             `json:"truncated"` // entities omitted past the cap (stated, never silent)
}

// TopologySummary is the headline rollup.
type TopologySummary struct {
	Nodes        int `json:"nodes"`
	Edges        int `json:"edges"`
	ValidEdges   int `json:"validEdges"`
	SuspectEdges int `json:"suspectEdges"`
	Matched      int `json:"matched"`  // nodes carrying a current phenomenon match
	Loud         int `json:"loud"`     // nodes carrying an unexplained loud card
	Selected     int `json:"selected"` // Tier-A nodes (selection earned them attention)
}

// TopoNode is one entity. Marks are MEASURED facts about the system's current
// state; each is a labelled boolean so the surface renders distinct glyphs.
type TopoNode struct {
	CEIKey    string   `json:"ceiKey"`
	Kind      string   `json:"kind"`
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Selected  bool     `json:"selected"`  // Tier-A (06)
	Matched   bool     `json:"matched"`   // a current phenomenon match (07) — "is"
	Degraded  bool     `json:"degraded"`  // the match(es) here are degraded
	Loud      bool     `json:"loud"`      // an unexplained loud card (08)
	Phenomena []string `json:"phenomena"` // matched phenomenon ids on this node
}

// TopoEdge is one topology edge with its validity verdict at `now`. A suspect
// edge is rendered visibly distinct (doc 03/10): detection degrades across it,
// it never silently supports.
type TopoEdge struct {
	Type   string `json:"type"`
	From   string `json:"from"`
	To     string `json:"to"`
	Status string `json:"status"` // valid | suspect | retracted
}

// topoCap bounds the rendered node set so a large cluster cannot produce an
// unusable surface; the omitted count is stated (Truncated), never hidden.
const topoCap = 400

// BuildTopology composes the topology surface from the inventory, the edge
// snapshot, the current findings + unexplained cards, and the Tier-A set. Pure
// given its inputs; cmd/obsd snapshots it each tick. budgets are the per-edge-
// type staleness budgets (doc 14 §1.2) — a live edge confirmed longer ago than
// its budget is SUSPECT, mirroring EdgeStore.Status exactly.
func BuildTopology(clusterID, graphVersion string, now time.Time,
	inventory []identity.InstanceRecord, edgeSnap []identity.EdgeSnap, budgets map[string]time.Duration,
	findings []detect.Finding, unexp []unexplained.Finding, selected map[string][]string) *TopologyView {

	v := &TopologyView{
		ClusterID: clusterID, GraphVersion: graphVersion, GeneratedAt: now.UTC(),
		Nodes: []TopoNode{}, Edges: []TopoEdge{},
	}

	// Current marks, indexed by entity CEI.
	matched := map[string][]string{} // entity -> matched phenomenon ids
	degraded := map[string]bool{}
	for i := range findings {
		f := &findings[i]
		matched[f.EntityCEI] = append(matched[f.EntityCEI], f.Phenomenon)
		if f.Quality == detect.QualityDegraded {
			degraded[f.EntityCEI] = true
		}
	}
	loud := map[string]bool{}
	for _, c := range unexp {
		if c.Status == unexplained.StatusNew || c.Status == unexplained.StatusAging {
			loud[c.Scope] = true
		}
	}

	// Nodes from the active inventory, capped. Sort first so truncation is
	// deterministic (matched/loud/selected entities are kept preferentially).
	type ranked struct {
		rec identity.InstanceRecord
		key string
	}
	rs := make([]ranked, 0, len(inventory))
	for _, rec := range inventory {
		rs = append(rs, ranked{rec, rec.CEI.Key()})
	}
	priority := func(key string) int {
		switch {
		case len(matched[key]) > 0:
			return 0
		case loud[key]:
			return 1
		case len(selected[key]) > 0:
			return 2
		default:
			return 3
		}
	}
	sort.Slice(rs, func(i, j int) bool {
		pi, pj := priority(rs[i].key), priority(rs[j].key)
		if pi != pj {
			return pi < pj
		}
		return rs[i].key < rs[j].key
	})

	included := map[string]bool{}
	for _, r := range rs {
		if len(included) >= topoCap {
			v.Truncated++
			continue
		}
		key := r.key
		included[key] = true
		phens := matched[key]
		sort.Strings(phens)
		node := TopoNode{
			CEIKey: key, Kind: r.rec.Kind, Namespace: r.rec.Namespace, Name: r.rec.Name,
			Selected: len(selected[key]) > 0, Matched: len(phens) > 0,
			Degraded: degraded[key], Loud: loud[key], Phenomena: phens,
		}
		if node.Phenomena == nil {
			node.Phenomena = []string{}
		}
		v.Nodes = append(v.Nodes, node)
		if node.Matched {
			v.Summary.Matched++
		}
		if node.Loud {
			v.Summary.Loud++
		}
		if node.Selected {
			v.Summary.Selected++
		}
	}
	sort.Slice(v.Nodes, func(i, j int) bool { return v.Nodes[i].CEIKey < v.Nodes[j].CEIKey })

	// Edges among included nodes only (an edge to a truncated entity would
	// dangle). Classify validity at `now`.
	for _, e := range edgeSnap {
		if !included[e.From] || !included[e.To] {
			continue
		}
		status := edgeStatus(e, budgets, now)
		v.Edges = append(v.Edges, TopoEdge{Type: e.Type, From: e.From, To: e.To, Status: status})
		switch status {
		case "valid":
			v.Summary.ValidEdges++
		case "suspect":
			v.Summary.SuspectEdges++
		}
	}
	sort.Slice(v.Edges, func(i, j int) bool {
		a, b := v.Edges[i], v.Edges[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.From != b.From {
			return a.From < b.From
		}
		return a.To < b.To
	})

	v.Summary.Nodes = len(v.Nodes)
	v.Summary.Edges = len(v.Edges)
	return v
}

// edgeStatus classifies a snapshot edge at `now` — mirrors EdgeStore.statusLocked
// (doc 03 §3.6): retracted edges report retracted; a live edge confirmed longer
// ago than its budget is suspect; otherwise valid. The default budget (5m)
// matches the edge store's fallback for unconfigured types.
func edgeStatus(e identity.EdgeSnap, budgets map[string]time.Duration, now time.Time) string {
	if !e.RetractedAt.IsZero() {
		return "retracted"
	}
	budget := 5 * time.Minute
	if b, ok := budgets[strings.TrimSpace(e.Type)]; ok {
		budget = b
	}
	if now.Sub(e.LastConfirmed) > budget {
		return "suspect"
	}
	return "valid"
}
