package flow

import (
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// EdgeTypeFlow is the package-local observed-flow edge type. It is DELIBERATELY
// absent from graph.knownTraversalEdgeTypes and params.requiredEdgeBudgets so it
// can never enter the production EdgeStore, an EdgeSnap, or the replay digest.
const EdgeTypeFlow identity.EdgeType = "flow"

// flowBudget keeps a freshly-observed edge VALID for the walk window. Long-lived
// gRPC channels sit at ~86400s ttl; the staleness judgement here is only about a
// snapshot's own window, so a generous budget is correct.
const flowBudget = 60 * time.Second

// FlowEdge is one reconstructed workload→workload observed call. Existence is
// MEASURED ("observed flow"); it is never surfaced as "depends-on".
type FlowEdge struct {
	From, To           identity.CEI // caller role, callee role
	FromLabel, ToLabel string       // namespace/workload, for surfacing only
	ServicePorts       map[int]bool
	ConnCount          int // distinct (callerIP,sport) observed across snapshots
	TickPresence       int // snapshots this edge appeared in (confidence signal)
}

// Coverage is the honest accounting of what conntrack could and could NOT recover.
type Coverage struct {
	ResolvableFlows int // candidate flows where both ends mapped to known workloads
	SnatMaskedFlows int // caller SNAT'd to a node bridge — unrecoverable, counted not guessed
	UnresolvedFlows int // candidate, but an endpoint IP was not a known workload
	InfraFlows      int // control-plane / DNS / host, excluded by policy
	UnrepliedFlows  int // no backend recovered (no reply tuple)
}

type edgeKey struct{ from, to string }
type connSeen struct {
	ip    string
	sport int
}

// Graph accumulates observed flow edges across one or more conntrack snapshots into
// its OWN identity.EdgeStore — never the production store.
type Graph struct {
	resolver *Resolver
	store    *identity.EdgeStore
	clock    func() time.Time
	edges    map[edgeKey]*FlowEdge
	conns    map[edgeKey]map[connSeen]bool
	cov      Coverage
	ticks    int
}

// NewGraph builds an empty flow graph with its own edge store.
func NewGraph(resolver *Resolver, clock func() time.Time) *Graph {
	budgets := map[identity.EdgeType]time.Duration{EdgeTypeFlow: flowBudget}
	return &Graph{
		resolver: resolver,
		store:    identity.NewEdgeStore(clock, budgets, time.Hour),
		clock:    clock,
		edges:    make(map[edgeKey]*FlowEdge),
		conns:    make(map[edgeKey]map[connSeen]bool),
	}
}

// Observe folds one conntrack snapshot (taken at instant `at`) into the graph:
// parse-recover each row, classify, resolve candidate flows to workload roles, skip
// self-edges, aggregate, and assert the edge into the store at `at`.
func (g *Graph) Observe(conns []Conn, at time.Time) {
	g.ticks++
	present := make(map[edgeKey]bool)
	for _, c := range conns {
		o := Recover(c)
		switch o.Class {
		case ClassSnatMasked:
			g.cov.SnatMaskedFlows++
			continue
		case ClassInfra:
			g.cov.InfraFlows++
			continue
		case ClassUnreplied:
			g.cov.UnrepliedFlows++
			continue
		}
		// Candidate: resolve both ends to workload roles.
		from, okF := g.resolver.Role(o.Caller)
		to, okT := g.resolver.Role(o.Callee)
		if !okF || !okT {
			g.cov.UnresolvedFlows++
			continue
		}
		if from.Key() == to.Key() {
			continue // intra-workload chatter is not a cross-service dependency
		}
		g.cov.ResolvableFlows++
		k := edgeKey{from.Key(), to.Key()}
		e := g.edges[k]
		if e == nil {
			fl, _ := g.resolver.Label(o.Caller)
			tl, _ := g.resolver.Label(o.Callee)
			e = &FlowEdge{From: from, To: to, FromLabel: fl, ToLabel: tl, ServicePorts: map[int]bool{}}
			g.edges[k] = e
			g.conns[k] = make(map[connSeen]bool)
		}
		e.ServicePorts[o.ServicePort] = true
		g.conns[k][connSeen{o.Caller, c.OrigSport}] = true
		if !present[k] {
			present[k] = true
			e.TickPresence++
		}
		g.store.Assert(EdgeTypeFlow, from, to, at)
	}
	for k, e := range g.edges {
		e.ConnCount = len(g.conns[k])
	}
}

// Edges returns the reconstructed edges, canonically sorted (caller, then callee).
func (g *Graph) Edges() []*FlowEdge {
	out := make([]*FlowEdge, 0, len(g.edges))
	for _, e := range g.edges {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FromLabel != out[j].FromLabel {
			return out[i].FromLabel < out[j].FromLabel
		}
		return out[i].ToLabel < out[j].ToLabel
	})
	return out
}

// Store exposes the flow edge store for the reverse-walk cascade.
func (g *Graph) Store() *identity.EdgeStore { return g.store }

// Coverage returns the honest recovery accounting.
func (g *Graph) Coverage() Coverage { return g.cov }

// Ticks is the number of snapshots folded in.
func (g *Graph) Ticks() int { return g.ticks }
