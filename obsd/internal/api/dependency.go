package api

import "time"

// DependencyClass labels the /api/dependency payload: MEASURED association, NOT
// causation (doc 20 §2.3). The edges are undirected (no causal arrow); only an
// AUTHORED relation legitimizes a direction.
const DependencyClass = "MEASURED association (associated-with; undirected; NOT causation)"

const dependencyNote = "The metric-dependency graph (doc 20 P2): observed series that move together over the " +
	"window, as MEASURED associations (Pearson correlation). These are NOT causal — an undirected association is a " +
	"candidate INPUT for authoring a causal relation, never the relation itself. This lane is off the deterministic " +
	"path and never feeds detection or forecasting."

const dependencyOffNote = "The metric-dependency lane is not enabled (--assoc-enabled). Detection and forecasting " +
	"are unaffected — assoc is off-digest and barred from the deterministic path."

// DependencyEdge is one MEASURED association, surfaced read-only. A and B are sorted
// (undirected). Coefficient is the Pearson r over the co-observed window.
type DependencyEdge struct {
	A           string  `json:"a"`
	B           string  `json:"b"`
	Relation    string  `json:"relation"`
	Coefficient float64 `json:"coefficient"`
	Overlap     int     `json:"overlap"`
}

// DependencyView is the /api/dependency payload.
type DependencyView struct {
	Class             string           `json:"class"`
	Available         bool             `json:"available"` // false ⇒ --assoc-enabled is off
	GeneratedAt       time.Time        `json:"generatedAt"`
	WindowStart       time.Time        `json:"windowStart"`
	WindowEnd         time.Time        `json:"windowEnd"`
	StreamsConsidered int              `json:"streamsConsidered"` // honest partial coverage: how many of
	StreamsTotal      int              `json:"streamsTotal"`      // the available streams were associated
	Edges             []DependencyEdge `json:"edges"`
	Note              string           `json:"note"`
}

// NewDependencyView builds the available view from already-mapped edges. considered/
// total make any stream-count bound honest (the surface states what it covered).
func NewDependencyView(now, windowStart, windowEnd time.Time, edges []DependencyEdge, considered, total int) *DependencyView {
	if edges == nil {
		edges = []DependencyEdge{}
	}
	return &DependencyView{
		Class: DependencyClass, Available: true, GeneratedAt: now,
		WindowStart: windowStart, WindowEnd: windowEnd,
		StreamsConsidered: considered, StreamsTotal: total,
		Edges: edges, Note: dependencyNote,
	}
}

// UnavailableDependency is the honest OFF state (the lane is not enabled).
func UnavailableDependency(now time.Time) *DependencyView {
	return &DependencyView{
		Class: DependencyClass, Available: false, GeneratedAt: now,
		Edges: []DependencyEdge{}, Note: dependencyOffNote,
	}
}
