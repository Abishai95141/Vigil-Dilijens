package identity

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Timestamped topology edges (doc 03 §3.5): an edge is an ASSERTION with a validity
// interval, never a bare fact. Each carries asserted-at, last-confirmed-at, and
// retracted-at. Two consequences govern the rest of the system:
//
//   - Per-edge-type staleness budgets (doc 14 §1.2): an edge confirmed longer ago
//     than its budget is SUSPECT and reported as such — never silently trusted.
//   - Walks intersect validity with the evaluation window (doc 07 §3.2): a
//     co-occurrence "across" an edge that was not valid during the window is NOT a
//     co-occurrence; a suspect edge DEGRADES a match rather than supporting it. This
//     single rule prevents detection from fabricating a 2-hop correlation through a
//     stale edge — the worst trust failure available to the product.
//
// Edges are between INSTANCE CEIs (this pod runs on this node); detection walks
// instance topology. No time.Now in logic: a clock is injected, and the traversal
// contract keys suspicion off the evaluation window, so it is replayable.

// EdgeType is a topology edge type. The string values match the parameters file's
// edge-budget keys (doc 14 §1.2) so budgets thread through by name.
type EdgeType string

const (
	EdgeRunsOn    EdgeType = "runs-on"    // pod -> node
	EdgeMounts    EdgeType = "mounts"     // pod -> PVC
	EdgeSelects   EdgeType = "selects"    // service -> pod
	EdgeOwns      EdgeType = "owns"       // controller ownership (reserved; role layer covers most uses)
	EdgeNodeLease EdgeType = "node-lease" // node liveness (self-edge; used via Status, not traversal)
)

// Traversal is the result of evaluating one edge against an evaluation window —
// the output of the validity-intersection contract.
type Traversal uint8

const (
	// TraversalAbsent: no such edge, or its validity does not overlap the window.
	// NOT a co-occurrence across this edge.
	TraversalAbsent Traversal = iota
	// TraversalSuspect: validity overlaps the window but confirmation is stale
	// (past budget as of the window end). DEGRADES a match.
	TraversalSuspect
	// TraversalValid: validity overlaps the window and confirmation is fresh (or the
	// edge is retracted but provably existed during the overlap). Supports a full match.
	TraversalValid
)

func (t Traversal) String() string {
	switch t {
	case TraversalAbsent:
		return "absent"
	case TraversalSuspect:
		return "suspect"
	case TraversalValid:
		return "valid"
	default:
		return "unknown"
	}
}

// EdgeStatus is a point-in-time classification of an edge (for surfacing/health),
// distinct from the window-relative Traversal.
type EdgeStatus uint8

const (
	StatusAbsent EdgeStatus = iota
	StatusValid
	StatusSuspect
	StatusRetracted
)

func (s EdgeStatus) String() string {
	switch s {
	case StatusValid:
		return "valid"
	case StatusSuspect:
		return "suspect"
	case StatusRetracted:
		return "retracted"
	default:
		return "absent"
	}
}

// EdgeAssertion is the timestamped-edge structure of doc 03 §3.6.
type EdgeAssertion struct {
	Type          EdgeType
	From          CEI
	To            CEI
	AssertedAt    time.Time
	LastConfirmed time.Time
	RetractedAt   time.Time // zero == live
}

func (e *EdgeAssertion) retracted() bool { return !e.RetractedAt.IsZero() }

// overlaps reports whether the edge's existence interval [AssertedAt, end) overlaps
// the inclusive window [start, end]. For a live edge the interval is open-ended.
func (e *EdgeAssertion) overlaps(start, end time.Time) bool {
	if e.AssertedAt.After(end) {
		return false // asserted after the window
	}
	if e.RetractedAt.IsZero() {
		return true // live and asserted by the window end
	}
	return e.RetractedAt.After(start) // retracted, but after the window start
}

// TimeWindow is a co-occurrence evaluation window (doc 07 §3.2).
type TimeWindow struct{ Start, End time.Time }

// Neighbour is a traversed neighbour with its traversal result.
type Neighbour struct {
	To     CEI
	Result Traversal
}

const edgeSep = "\x1f" // unit separator; never appears in a CEI key

func edgeKey(typ EdgeType, fromKey, toKey string) string {
	return string(typ) + edgeSep + fromKey + edgeSep + toKey
}

// EdgeStore holds topology edges as timestamped assertions and answers the
// validity-intersection traversal contract.
type EdgeStore struct {
	mu               sync.RWMutex
	clock            func() time.Time
	budgets          map[EdgeType]time.Duration
	defaultBudget    time.Duration
	retractedHorizon time.Duration

	edges  map[string]*EdgeAssertion   // edgeKey -> assertion
	byFrom map[string][]*EdgeAssertion // fromKey -> assertions (neighbour walks, source retraction)
	byTo   map[string][]*EdgeAssertion // toKey   -> assertions (target retraction)

	mAsserted          atomic.Int64
	mConfirmed         atomic.Int64
	mRetracted         atomic.Int64
	mReasserted        atomic.Int64
	mSuspectTraversals atomic.Int64
	mAbsentTraversals  atomic.Int64
}

// NewEdgeStore builds an edge store. budgets are the per-edge-type staleness budgets
// (doc 14 §1.2, from the parameters file); retractedHorizon is how long retracted
// edges stay queryable in memory (doc 14 §1.3). A nil clock defaults to time.Now.
func NewEdgeStore(clock func() time.Time, budgets map[EdgeType]time.Duration, retractedHorizon time.Duration) *EdgeStore {
	if clock == nil {
		clock = time.Now
	}
	cp := make(map[EdgeType]time.Duration, len(budgets))
	for k, v := range budgets {
		cp[k] = v
	}
	return &EdgeStore{
		clock:            clock,
		budgets:          cp,
		defaultBudget:    5 * time.Minute, // fallback only; all real types are configured
		retractedHorizon: retractedHorizon,
		edges:            make(map[string]*EdgeAssertion),
		byFrom:           make(map[string][]*EdgeAssertion),
		byTo:             make(map[string][]*EdgeAssertion),
	}
}

func (s *EdgeStore) budgetFor(typ EdgeType) time.Duration {
	if b, ok := s.budgets[typ]; ok {
		return b
	}
	return s.defaultBudget
}

// Assert records or confirms an edge at time `at` (a watch event or reconciliation
// confirmation, doc 14 §1.1). A new edge is created; a live edge has its
// last-confirmed advanced (monotonically); a retracted edge that reappears is
// re-asserted with a fresh interval.
func (s *EdgeStore) Assert(typ EdgeType, from, to CEI, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.assertLocked(typ, from, to, at)
}

func (s *EdgeStore) assertLocked(typ EdgeType, from, to CEI, at time.Time) {
	k := edgeKey(typ, from.Key(), to.Key())
	e, ok := s.edges[k]
	if !ok {
		e = &EdgeAssertion{Type: typ, From: from, To: to, AssertedAt: at, LastConfirmed: at}
		s.edges[k] = e
		s.byFrom[from.Key()] = append(s.byFrom[from.Key()], e)
		s.byTo[to.Key()] = append(s.byTo[to.Key()], e)
		s.mAsserted.Add(1)
		return
	}
	if e.retracted() {
		// Re-assert only FORWARD of the retraction. A stale event older than the
		// retraction — e.g. a resync re-delivering an old lease RenewTime after the
		// edge was retracted — must not resurrect the edge or move confirmation
		// backwards (which would falsely revive a dead node's liveness).
		if !at.After(e.RetractedAt) {
			return
		}
		e.AssertedAt = at
		e.RetractedAt = time.Time{}
		e.LastConfirmed = at
		s.mReasserted.Add(1)
		return
	}
	if at.After(e.LastConfirmed) {
		e.LastConfirmed = at
	}
	s.mConfirmed.Add(1)
}

// Retract marks an edge retracted at `at` (its source object was deleted, or it was
// reconciled away). It remains queryable for the retracted horizon (doc 14 §1.3).
func (s *EdgeStore) Retract(typ EdgeType, fromKey, toKey string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.edges[edgeKey(typ, fromKey, toKey)]; ok {
		s.retractLocked(e, at)
	}
}

func (s *EdgeStore) retractLocked(e *EdgeAssertion, at time.Time) {
	if e.retracted() {
		return
	}
	if at.Before(e.AssertedAt) {
		at = e.AssertedAt // no negative-length validity
	}
	e.RetractedAt = at
	s.mRetracted.Add(1)
}

// ReconcileOut asserts every edge (from -> to) for the current target set and
// retracts any live (from -> *) edge of this type whose target is no longer present.
// Used where one source's full out-set is known at once (e.g. a pod's single node
// for runs-on). currentTo may be empty (retracts all of this type from `from`).
func (s *EdgeStore) ReconcileOut(typ EdgeType, from CEI, currentTo []CEI, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := make(map[string]struct{}, len(currentTo))
	for _, to := range currentTo {
		want[to.Key()] = struct{}{}
		s.assertLocked(typ, from, to, at)
	}
	for _, e := range s.byFrom[from.Key()] {
		if e.Type != typ || e.retracted() {
			continue
		}
		if _, keep := want[e.To.Key()]; !keep {
			s.retractLocked(e, at)
		}
	}
}

// RetractAllFrom retracts every live edge whose source is the given CEI (e.g. a pod
// deleted -> its runs-on and mounts go away).
func (s *EdgeStore) RetractAllFrom(fromKey string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.byFrom[fromKey] {
		s.retractLocked(e, at)
	}
}

// RetractAllTo retracts every live edge whose target is the given CEI (e.g. a node
// deleted -> the runs-on edges into it go away).
func (s *EdgeStore) RetractAllTo(toKey string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.byTo[toKey] {
		s.retractLocked(e, at)
	}
}

// Traverse evaluates one edge against an evaluation window — THE validity-
// intersection contract (doc 07 §3.2). The result is deterministic given the store
// state and the window (suspicion is keyed off the window end, not wall-clock).
func (s *EdgeStore) Traverse(typ EdgeType, fromKey, toKey string, w TimeWindow) Traversal {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.edges[edgeKey(typ, fromKey, toKey)]
	if !ok {
		// Absent — including retracted edges already reclaimed by GC past their
		// queryable horizon (doc 14 §1.3). The horizon is a memory-reclamation
		// boundary owned by GC, NOT a traversal input: this keeps Traverse a pure
		// function of (store state, window) and therefore replayable.
		s.mAbsentTraversals.Add(1)
		return TraversalAbsent
	}
	if !e.overlaps(w.Start, w.End) {
		s.mAbsentTraversals.Add(1)
		return TraversalAbsent
	}
	// Existence overlaps the window. A retracted edge that overlapped it provably
	// existed during the overlap — we have ground truth of its interval, so it
	// supports (no suspicion). A live edge is suspect if its last confirmation is
	// older than its budget as of the window end.
	if e.retracted() {
		return TraversalValid
	}
	if w.End.Sub(e.LastConfirmed) > s.budgetFor(typ) {
		s.mSuspectTraversals.Add(1)
		return TraversalSuspect
	}
	return TraversalValid
}

// Status classifies an edge at instant `at` (for the topology view / health).
func (s *EdgeStore) Status(typ EdgeType, fromKey, toKey string, at time.Time) EdgeStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.edges[edgeKey(typ, fromKey, toKey)]
	if !ok {
		return StatusAbsent
	}
	return s.statusLocked(e, at)
}

func (s *EdgeStore) statusLocked(e *EdgeAssertion, at time.Time) EdgeStatus {
	// A retracted edge still resident in memory reports Retracted; once GC reclaims
	// it past the horizon it is absent (not in the map). Status is thus a pure
	// function of (state, at) — no wall-clock coupling.
	if e.retracted() {
		return StatusRetracted
	}
	if at.Sub(e.LastConfirmed) > s.budgetFor(e.Type) {
		return StatusSuspect
	}
	return StatusValid
}

// NodeLiveness returns the node-lease status for a node CEI as of `at` — a node
// whose lease has not renewed within its budget (40 s, doc 14 §1.2) is suspect,
// mirroring the control plane's own node-monitor grace period.
func (s *EdgeStore) NodeLiveness(nodeKey string, at time.Time) EdgeStatus {
	return s.Status(EdgeNodeLease, nodeKey, nodeKey, at)
}

// Neighbours returns the traversable neighbours of `fromKey` along an edge type,
// honouring the validity-intersection contract over the window. Absent (non-valid)
// edges are omitted; suspect ones are included with their result so callers
// (selection 06, detection 07) can degrade accordingly. Sorted by target key —
// neighbour order must be canonical or replayed walks would not be byte-identical
// (byFrom holds assertion order, which differs between a live store and one
// rebuilt from a snapshot).
func (s *EdgeStore) Neighbours(typ EdgeType, fromKey string, w TimeWindow) []Neighbour {
	s.mu.RLock()
	candidates := make([]*EdgeAssertion, 0)
	for _, e := range s.byFrom[fromKey] {
		if e.Type == typ {
			candidates = append(candidates, e)
		}
	}
	s.mu.RUnlock()

	out := make([]Neighbour, 0, len(candidates))
	for _, e := range candidates {
		r := s.Traverse(typ, fromKey, e.To.Key(), w)
		if r != TraversalAbsent {
			out = append(out, Neighbour{To: e.To, Result: r})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].To.Key() < out[j].To.Key() })
	return out
}

// NeighboursInto is the reverse walk: the traversable SOURCES of edges pointing
// at `toKey` (e.g. the pods whose runs-on edges land on a node — the node-anchored
// first-order walk of doc 07 §3.2). Same validity contract, same canonical order.
func (s *EdgeStore) NeighboursInto(typ EdgeType, toKey string, w TimeWindow) []Neighbour {
	s.mu.RLock()
	candidates := make([]*EdgeAssertion, 0)
	for _, e := range s.byTo[toKey] {
		if e.Type == typ {
			candidates = append(candidates, e)
		}
	}
	s.mu.RUnlock()

	out := make([]Neighbour, 0, len(candidates))
	for _, e := range candidates {
		r := s.Traverse(typ, e.From.Key(), toKey, w)
		if r != TraversalAbsent {
			out = append(out, Neighbour{To: e.From, Result: r})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].To.Key() < out[j].To.Key() })
	return out
}

// Sources returns the distinct source keys holding at least one resident
// assertion of the given type, sorted. Residency only — validity is the
// traversal's judgement, per window. Used to enumerate walk origins (e.g. every
// pod with a runs-on edge, for container→pod containment resolution).
func (s *EdgeStore) Sources(typ EdgeType) []string {
	s.mu.RLock()
	set := map[string]bool{}
	for _, e := range s.edges {
		if e.Type == typ {
			set[e.From.Key()] = true
		}
	}
	s.mu.RUnlock()
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// GC drops retracted edges past the retained horizon (doc 14 §1.3). The on-disk
// topology log that would receive them (the replay-bundle topology snapshot, doc 11)
// is a follow-on.
func (s *EdgeStore) GC() {
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, e := range s.edges {
		if e.retracted() && now.Sub(e.RetractedAt) >= s.retractedHorizon {
			delete(s.edges, k)
			s.byFrom[e.From.Key()] = removeEdge(s.byFrom[e.From.Key()], e)
			if len(s.byFrom[e.From.Key()]) == 0 {
				delete(s.byFrom, e.From.Key())
			}
			s.byTo[e.To.Key()] = removeEdge(s.byTo[e.To.Key()], e)
			if len(s.byTo[e.To.Key()]) == 0 {
				delete(s.byTo, e.To.Key())
			}
		}
	}
}

func removeEdge(list []*EdgeAssertion, e *EdgeAssertion) []*EdgeAssertion {
	for i, x := range list {
		if x == e {
			return append(list[:i], list[i+1:]...)
		}
	}
	return list
}

// EdgeTypeMetrics is the per-type staleness breakdown (doc 03 §6).
type EdgeTypeMetrics struct {
	Live      int
	Suspect   int
	Retracted int
}

// EdgeMetrics is a snapshot of the edge store's health signals.
type EdgeMetrics struct {
	Live              int
	Suspect           int
	Retracted         int
	PerType           map[EdgeType]EdgeTypeMetrics
	Asserted          int64
	Confirmed         int64
	Retractions       int64
	Reasserted        int64
	SuspectTraversals int64
	AbsentTraversals  int64
}

// Metrics returns a snapshot. Staleness is computed as of now.
func (s *EdgeStore) Metrics() EdgeMetrics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := s.clock()
	m := EdgeMetrics{PerType: make(map[EdgeType]EdgeTypeMetrics)}
	for _, e := range s.edges {
		pt := m.PerType[e.Type]
		switch s.statusLocked(e, now) {
		case StatusValid:
			m.Live++
			pt.Live++
		case StatusSuspect:
			m.Suspect++
			pt.Suspect++
		case StatusRetracted:
			m.Retracted++
			pt.Retracted++
		}
		m.PerType[e.Type] = pt
	}
	m.Asserted = s.mAsserted.Load()
	m.Confirmed = s.mConfirmed.Load()
	m.Retractions = s.mRetracted.Load()
	m.Reasserted = s.mReasserted.Load()
	m.SuspectTraversals = s.mSuspectTraversals.Load()
	m.AbsentTraversals = s.mAbsentTraversals.Load()
	return m
}
