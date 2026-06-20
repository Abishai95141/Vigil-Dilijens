// Package graph loads the curated ontology knowledge graph (the authoritative
// type-level artifact, doc 02) into typed, indexed in-memory structures that
// binding (04) and detection (07) read. It also holds the bound customer graph
// (04) — added in Phase 0b.
//
// The on-disk artifact is the 842-node KG: a {nodes, edges} graph with 10 node
// types and 12 edge types (doc 02 §2, doc 14 A14). This loader parses it, resolves
// phenomenon membership and relations from the structured edges, and content-hashes
// the release for version pinning (doc 12 §3.1). Everything loaded is AUTHORED-class
// by construction (doc 02 §4): there is no field where a measurement or model output
// could live, and nothing here learns.
package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// --- node types (doc 02 §3.1) ------------------------------------------------

// Signal is a "Signal" node — the unit of authorship (doc 02 §3.3 Variable).
type Signal struct {
	ID                string                 `json:"id"`
	Name              string                 `json:"name"`
	Modality          string                 `json:"modality"`  // Metric is the only numeric series (doc 02 §2)
	DataType          string                 `json:"data_type"` // free-text in the KG; forecast-eligibility is DERIVED
	Category          string                 `json:"category"`
	Sheet             string                 `json:"sheet"` // source-spreadsheet provenance
	Source            string                 `json:"source"`
	CollectionMethod  string                 `json:"collection_method"`
	Granularity       string                 `json:"granularity"`
	UpdateFreq        string                 `json:"update_freq"`
	Tier              string                 `json:"tier"`
	RawOrDerived      string                 `json:"raw_or_derived"`
	ToolRequired      string                 `json:"tool_required"`
	Notes             string                 `json:"notes"`
	Entity            string                 `json:"entity"`
	Tools             []string               `json:"tools"`
	EquivalenceGroups []string               `json:"equivalence_groups"`
	Capabilities      []string               `json:"capabilities"`
	DistroGates       []string               `json:"distro_gates"`
	Gotchas           []string               `json:"gotchas"`
	CorrelationGroups []SignalCorrelationRef `json:"correlation_groups"`
	DerivationChains  []DerivationChain      `json:"derivation_chains"`
}

// SignalCorrelationRef is the signal-side denormalized view of a participates_in
// relationship (mirrors the participates_in edges; the edges are authoritative).
type SignalCorrelationRef struct {
	CorrID        string `json:"corr_id"`
	Role          string `json:"role"`
	TemporalOrder string `json:"temporal_order"`
	Why           string `json:"why"`
	SignalPattern string `json:"signal_pattern"`
}

// DerivationChain records how a signal is derived from others (mirrors derived_from edges).
type DerivationChain struct {
	Derived     string   `json:"derived"`
	DerivedFrom []string `json:"derived_from"`
	How         string   `json:"how"`
}

// IsMetric reports whether this signal is a numeric time series (the only
// forecastable modality, doc 02 §2).
func (s *Signal) IsMetric() bool { return s.Modality == "Metric" }

// Forecastable derives forecast-eligibility in principle (doc 09 §3.2): a
// Metric whose canonical series shape contains a gauge or a counter (counters
// only via rate derivation, flagged second-class by the funnel). Derived from
// the authored data_type via ParseSeriesShape (seriesshape.go) — the funnel's
// remaining per-target gates (bound, real dynamics, resolvable bar, precursor)
// are runtime properties owned by doc 09 M2.
func (s *Signal) Forecastable() bool {
	if !s.IsMetric() {
		return false
	}
	sh := s.Shape()
	return sh.Gauge || sh.Counter
}

// InlineMember is one authored member tuple from a phenomenon's signals[] array:
// [pattern, role, temporal_tag, note] (doc 02 §3.5). The pattern is a regex over
// customer metric names, not a resolved signal id.
type InlineMember struct {
	Pattern     string
	Role        string // required | corroborating
	TemporalTag string // T0-, T0, T0+restart, ... (rich vocabulary, free-form)
	Note        string
}

// Member is a resolved phenomenon member from a participates_in edge: an actual
// Signal node, with its role and temporal order.
type Member struct {
	SignalID      string
	Role          string // required | corroborating | downstream
	TemporalOrder string
	Why           string
}

// Relation is a phenomenon→phenomenon edge (cascades and blast radius, doc 02 §3.5).
type Relation struct {
	TargetID      string
	Role          string // trigger | downstream | corroborating
	TemporalOrder string
	Why           string
}

// Phenomenon is a "CorrelationGroup" node — a named multi-signal pattern (doc 02 §3.5).
//
// NOTE (the doc 14 A14 gap): the KG's phenomena carry temporal tags but NO declared
// topological span or traversal edge types. Span/TraversalEdgeTypes are therefore
// zero here until authored; doc 02 §3.6 says an undeclared span is INVALID, not
// entity-local-by-default — graphlint flags every phenomenon missing one.
type Phenomenon struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	Notes      string     `json:"notes"`
	RawSignals [][]string `json:"signals"`

	// Severity is an AUTHORED prioritisation level (doc 21 Phase 5): how much HARM this
	// phenomenon represents when it fires, declared by the curator (borrowed normativity — never
	// learned, never model-assigned). It is a RANKING input for governance + the agent, NEVER a
	// detection gate (detection ignores it, so it does not perturb the replay digest). The closed
	// vocabulary is critical > high > medium > low; "" means UNDECLARED — honest partial coverage,
	// not every phenomenon has a curated severity yet.
	Severity string `json:"severity"`

	// span/traversal: present in the schema for forward-compat; absent in today's KG.
	Span               string   `json:"span"`
	TraversalEdgeTypes []string `json:"traversal_edge_types"`

	// Anchor is the entity kind a spanned (first/second-order) phenomenon is
	// evaluated AT (doc 07 §3.2: "the entity plus direct neighbours") — authored
	// in the detection-conditions overlay, never inferred. Empty for entity-local
	// phenomena (the anchor is wherever the variables live).
	Anchor string `json:"-"`

	// Derived (not from JSON):
	InlineMembers []InlineMember `json:"-"`
	Members       []Member       `json:"-"` // from participates_in edges
	Relations     []Relation     `json:"-"` // from phenomenon_relation edges
}

// HasSpan reports whether the phenomenon declares a topological span (doc 02 §3.6).
func (p *Phenomenon) HasSpan() bool { return strings.TrimSpace(p.Span) != "" }

// The closed AUTHORED severity vocabulary (doc 21 Phase 5). Ordered by harm.
const (
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
)

// IsValidSeverity reports whether s is a declared level or "" (undeclared). An unknown value is
// an authoring defect (a curator typo) — caught loudly by graphlint and the overlay loader.
func IsValidSeverity(s string) bool {
	switch s {
	case "", SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow:
		return true
	}
	return false
}

// SeverityRank orders severities for prioritisation (higher = more harm); "" (undeclared) sorts
// LAST so a curated phenomenon always ranks above an unranked one. Pure: a deterministic map of
// an authored label to an integer, never a learned weight.
func SeverityRank(s string) int {
	switch s {
	case SeverityCritical:
		return 4
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	default:
		return 0 // undeclared
	}
}

// EquivalenceGroup bridges customer naming dialects to one canonical variable
// (doc 02 §3.1): a set of regex patterns plus a canonical name.
type EquivalenceGroup struct {
	ID            string   `json:"id"`
	Label         string   `json:"label"`
	CanonicalOTel string   `json:"canonical_otel"`
	Patterns      []string `json:"patterns"`
	Notes         string   `json:"notes"`
}

// Gotcha is an authored trap/honeypot (semantic caveats for binding QA, doc 04 §3.2).
type Gotcha struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	AppliesTo   string `json:"applies_to"`
	Condition   string `json:"condition"`
	Evidence    string `json:"evidence"`
	Implication string `json:"implication"`
}

// Node is the generic shape for the simpler node types (Entity, Tool, Modality,
// CapabilityPrereq, DistroVersionGate, Agent).
type Node struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Edge is a typed graph edge. All edges share src/dst/types; the remaining fields
// are populated per edge type (role/temporal_order on participates_in and
// phenomenon_relation; how on derived_from; threshold on owned_by_agent).
type Edge struct {
	Type          string `json:"type"`
	Src           string `json:"src"`
	SrcType       string `json:"src_type"`
	Dst           string `json:"dst"`
	DstType       string `json:"dst_type"`
	Role          string `json:"role"`
	TemporalOrder string `json:"temporal_order"`
	Why           string `json:"why"`
	How           string `json:"how"`
	Threshold     string `json:"threshold"`
}

// Graph is the loaded ontology release.
type Graph struct {
	Version string // content hash of the release ("sha256:...") — the pin (doc 12 §3.1)
	Release string // human release name (e.g. "v0.1.0") when loaded via a release manifest; "" for an unversioned dev load
	Meta    map[string]any
	Stats   map[string]any

	Signals           map[string]*Signal
	Phenomena         map[string]*Phenomenon
	EquivalenceGroups map[string]*EquivalenceGroup
	Gotchas           map[string]*Gotcha
	Entities          map[string]*Node
	Tools             map[string]*Node
	Modalities        map[string]*Node
	Capabilities      map[string]*Node
	DistroGates       map[string]*Node
	Agents            map[string]*Node

	Edges       []Edge
	edgesByType map[string][]*Edge
	edgesBySrc  map[string][]*Edge
	edgesByDst  map[string][]*Edge

	// Authored overlay content (doc 02 §3.6, merged by LoadWithOverlays): structured
	// threshold rules (sorted by ID), detection member-checks (per phenomenon, doc 07),
	// and the provenance of every applied overlay.
	Rules     []*ThresholdRule
	rulesByID map[string]*ThresholdRule
	Checks    map[string][]*MemberCheck // phenomenon id -> authored member checks
	Overlays  []OverlayInfo
}

type kgFile struct {
	Meta  map[string]any    `json:"_meta"`
	Stats map[string]any    `json:"stats"`
	Nodes []json.RawMessage `json:"nodes"`
	Edges []Edge            `json:"edges"`
}

// Load reads and parses an ontology KG release from a file.
func Load(path string) (*Graph, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read ontology graph %q: %w", path, err)
	}
	g, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse ontology graph %q: %w", path, err)
	}
	return g, nil
}

// Parse parses raw KG JSON into a typed, indexed Graph and pins its content hash.
func Parse(raw []byte) (*Graph, error) {
	var f kgFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("unmarshal graph: %w", err)
	}
	g := &Graph{
		Version:           "sha256:" + contentHash(raw),
		Meta:              f.Meta,
		Stats:             f.Stats,
		Signals:           map[string]*Signal{},
		Phenomena:         map[string]*Phenomenon{},
		EquivalenceGroups: map[string]*EquivalenceGroup{},
		Gotchas:           map[string]*Gotcha{},
		Entities:          map[string]*Node{},
		Tools:             map[string]*Node{},
		Modalities:        map[string]*Node{},
		Capabilities:      map[string]*Node{},
		DistroGates:       map[string]*Node{},
		Agents:            map[string]*Node{},
		edgesByType:       map[string][]*Edge{},
		edgesBySrc:        map[string][]*Edge{},
		edgesByDst:        map[string][]*Edge{},
		rulesByID:         map[string]*ThresholdRule{},
		Checks:            map[string][]*MemberCheck{},
	}

	for i, nraw := range f.Nodes {
		var probe struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(nraw, &probe); err != nil {
			return nil, fmt.Errorf("node %d: %w", i, err)
		}
		if probe.ID == "" || probe.Type == "" {
			return nil, fmt.Errorf("node %d: missing id or type", i)
		}
		if err := g.addNode(probe.Type, nraw); err != nil {
			return nil, fmt.Errorf("node %d (%s): %w", i, probe.ID, err)
		}
	}

	g.Edges = f.Edges
	g.reindexEdges()
	g.resolvePhenomena()
	if err := g.checkStats(); err != nil {
		return nil, err
	}
	return g, nil
}

// checkStats cross-checks the release's own declared totals (._meta/.stats) against
// what was actually parsed — catching a truncated or partially-written release at
// ingestion (exactly this layer's job). Absent stats are skipped.
func (g *Graph) checkStats() error {
	check := func(key string, parsed int) error {
		v, ok := g.Stats[key].(float64)
		if !ok {
			return nil
		}
		if int(v) != parsed {
			return fmt.Errorf("stats.%s = %d but parsed %d (truncated or corrupt release?)", key, int(v), parsed)
		}
		return nil
	}
	if err := check("nodes_total", g.NodeCount()); err != nil {
		return err
	}
	if err := check("edges_total", len(g.Edges)); err != nil {
		return err
	}
	return check("signals", len(g.Signals))
}

func (g *Graph) addNode(typ string, raw json.RawMessage) error {
	switch typ {
	case "Signal":
		var s Signal
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		g.Signals[s.ID] = &s
	case "CorrelationGroup":
		var p Phenomenon
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		for _, tup := range p.RawSignals {
			if len(tup) == 4 {
				p.InlineMembers = append(p.InlineMembers, InlineMember{Pattern: tup[0], Role: tup[1], TemporalTag: tup[2], Note: tup[3]})
			}
		}
		g.Phenomena[p.ID] = &p
	case "EquivalenceGroup":
		var eg EquivalenceGroup
		if err := json.Unmarshal(raw, &eg); err != nil {
			return err
		}
		g.EquivalenceGroups[eg.ID] = &eg
	case "Gotcha":
		var gotcha Gotcha
		if err := json.Unmarshal(raw, &gotcha); err != nil {
			return err
		}
		g.Gotchas[gotcha.ID] = &gotcha
	default:
		var n Node
		if err := json.Unmarshal(raw, &n); err != nil {
			return err
		}
		switch typ {
		case "Entity":
			g.Entities[n.ID] = &n
		case "Tool":
			g.Tools[n.ID] = &n
		case "Modality":
			g.Modalities[n.ID] = &n
		case "CapabilityPrereq":
			g.Capabilities[n.ID] = &n
		case "DistroVersionGate":
			g.DistroGates[n.ID] = &n
		case "Agent":
			g.Agents[n.ID] = &n
		default:
			return fmt.Errorf("unknown node type %q", typ)
		}
	}
	return nil
}

// reindexEdges rebuilds the by-type/src/dst pointer indexes from g.Edges. Called
// after the base load and again whenever an overlay appends edges (doc 15 Phase C),
// since a slice growth re-homes the backing array and would dangle stale pointers.
func (g *Graph) reindexEdges() {
	g.edgesByType = map[string][]*Edge{}
	g.edgesBySrc = map[string][]*Edge{}
	g.edgesByDst = map[string][]*Edge{}
	for i := range g.Edges {
		e := &g.Edges[i]
		g.edgesByType[e.Type] = append(g.edgesByType[e.Type], e)
		g.edgesBySrc[e.Src] = append(g.edgesBySrc[e.Src], e)
		g.edgesByDst[e.Dst] = append(g.edgesByDst[e.Dst], e)
	}
}

// resolvePhenomena attaches members (participates_in) and relations
// (phenomenon_relation) to their phenomena. Edges referencing an unknown
// phenomenon are left for graphlint to report; the loader stays lenient.
func (g *Graph) resolvePhenomena() {
	for i := range g.Edges {
		e := &g.Edges[i]
		switch e.Type {
		case "participates_in":
			if p := g.Phenomena[e.Dst]; p != nil {
				p.Members = append(p.Members, Member{SignalID: e.Src, Role: e.Role, TemporalOrder: e.TemporalOrder, Why: e.Why})
			}
		case "phenomenon_relation":
			if p := g.Phenomena[e.Src]; p != nil {
				p.Relations = append(p.Relations, Relation{TargetID: e.Dst, Role: e.Role, TemporalOrder: e.TemporalOrder, Why: e.Why})
			}
		}
	}
}

// EdgesByType returns all edges of a given type.
func (g *Graph) EdgesByType(typ string) []*Edge { return g.edgesByType[typ] }

// EdgesFrom returns all edges whose source is the given node id.
func (g *Graph) EdgesFrom(id string) []*Edge { return g.edgesBySrc[id] }

// EdgesTo returns all edges whose destination is the given node id.
func (g *Graph) EdgesTo(id string) []*Edge { return g.edgesByDst[id] }

// NodeType returns the type of a node by id, if present.
func (g *Graph) NodeType(id string) (string, bool) {
	switch {
	case g.Signals[id] != nil:
		return "Signal", true
	case g.Phenomena[id] != nil:
		return "CorrelationGroup", true
	case g.EquivalenceGroups[id] != nil:
		return "EquivalenceGroup", true
	case g.Gotchas[id] != nil:
		return "Gotcha", true
	case g.Entities[id] != nil:
		return "Entity", true
	case g.Tools[id] != nil:
		return "Tool", true
	case g.Modalities[id] != nil:
		return "Modality", true
	case g.Capabilities[id] != nil:
		return "CapabilityPrereq", true
	case g.DistroGates[id] != nil:
		return "DistroVersionGate", true
	case g.Agents[id] != nil:
		return "Agent", true
	default:
		return "", false
	}
}

// NodeCount is the total number of nodes loaded.
func (g *Graph) NodeCount() int {
	return len(g.Signals) + len(g.Phenomena) + len(g.EquivalenceGroups) + len(g.Gotchas) +
		len(g.Entities) + len(g.Tools) + len(g.Modalities) + len(g.Capabilities) +
		len(g.DistroGates) + len(g.Agents)
}

// MetricSignals returns the numeric-series signals (the forecastable modality).
func (g *Graph) MetricSignals() []*Signal {
	var out []*Signal
	for _, s := range g.Signals {
		if s.IsMetric() {
			out = append(out, s)
		}
	}
	return out
}

func contentHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
