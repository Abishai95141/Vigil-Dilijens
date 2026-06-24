package identity

import (
	"container/heap"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Lifecycle state machine + succession (doc 03 §3.4) and the two-tier tombstone
// store (doc 14 §1.4). This is the time-aware control-plane view that the
// normalizer's Lookup consumes: "which pod/node was (ns,name) at instant t".
//
// The store is the single point of referential truth at runtime. Its discipline:
//   - A restart is a new instance under the same role; rescheduling never changes
//     identity; role continuity survives any instance event.
//   - Tombstones are retained long enough that a LATE sample joins the correct,
//     now-dead instance rather than a same-name successor (the join honeypot,
//     doc 14 §3.2). Two tiers: full record (doc 14 §1.4: 15 min) then a degraded
//     stub (24 h), with a hard LRU cap as a safety valve.
//
// No time.Now in logic: a clock is injected. Lookup correctness is independent of
// when (or whether) GC has run — the retention horizons are enforced at read time.

// LifecycleState is the per-instance lifecycle state (doc 03 §3.4).
type LifecycleState uint8

const (
	StateDiscovered LifecycleState = iota + 1 // known to exist, not yet observed running
	StateActive                               // observed running
	StateTerminated                           // dead (tombstoned)
)

func (s LifecycleState) String() string {
	switch s {
	case StateDiscovered:
		return "discovered"
	case StateActive:
		return "active"
	case StateTerminated:
		return "terminated"
	default:
		return "unknown"
	}
}

// Tier classifies a dead instance's tombstone tier at a given instant (doc 14 §1.4).
type Tier uint8

const (
	TierAlive   Tier = iota
	TierFull         // dead < fullRetention: complete record
	TierStub         // fullRetention <= dead < stubRetention: correct CEI, degraded detail
	TierExpired      // dead >= stubRetention: out of the in-memory horizon (does not resolve)
)

// InstanceRecord is the identity lifecycle record (doc 03 §3.6).
type InstanceRecord struct {
	CEI     CEI
	RoleCEI CEI // zero for kinds with no role layer (e.g. Node)
	State   LifecycleState
	BornAt  time.Time // the object's own creation time (CreationTimestamp)
	DiedAt  time.Time // zero == alive

	Predecessor string // CEI key of the prior same-name instance (succession)
	Successor   string // CEI key of the next same-name instance

	// Coordinates, retained across tiers for lookup and reconstruction.
	Kind, Namespace, Name, UID string

	heapIdx int // index in the dead heap; -1 when not heaped
}

// alive and coversInstant are value receivers so a snapshot copy returned by Get
// can be read safely without holding the store lock (the store keeps mutating its
// own *InstanceRecord pointers under lock).
func (r InstanceRecord) alive() bool { return r.DiedAt.IsZero() }

// coversInstant reports whether this instance was alive at instant t — half-open
// [BornAt, DiedAt). An instant exactly at death belongs to the successor (or to
// nothing), never to both.
func (r InstanceRecord) coversInstant(t time.Time) bool {
	if t.Before(r.BornAt) {
		return false
	}
	if r.DiedAt.IsZero() {
		return true
	}
	return t.Before(r.DiedAt)
}

func nameKey(kind, namespace, name string) string {
	return kind + "|" + namespace + "|" + name
}

// Metrics is a snapshot of the store's published health signals (doc 03 §6,
// doc 14 §1.4). Counters are monotonic; gauges reflect the current population.
type Metrics struct {
	Active         int // alive instances
	FullTombstones int // dead, full tier
	StubTombstones int // dead, stub tier
	Discovered     int64
	Terminated     int64
	Successions    int64
	DegradedJoins  int64 // late-sample joins that landed on a stub-tier tombstone
	// EvictedBeforeHorizon counts tombstones dropped by the LRU cap while still
	// within their retention horizon — a join-risk signal that must never be
	// silent (doc 14 §1.4: tombstones_evicted_before_horizon).
	EvictedBeforeHorizon int64
	Archived             int64 // tombstones aged out past stubRetention (to be logged to disk)
}

// *Store is the production backing for the normalizer's time-aware Lookup — this
// is the M2<->M3 join made a compile-time guarantee.
var _ Lookup = (*Store)(nil)

// Store holds the lifecycle records and serves time-aware identity lookups.
// It implements Lookup, so *Store is the production backing for the normalizer.
type Store struct {
	mu            sync.RWMutex
	clock         func() time.Time
	fullRetention time.Duration
	stubRetention time.Duration
	maxEntries    int

	byCEI  map[string]*InstanceRecord   // CEI key -> record (alive and dead)
	byName map[string][]*InstanceRecord // (kind|ns|name) -> records ordered by BornAt
	dead   deadHeap                     // min-heap of dead records by DiedAt

	mDiscovered           atomic.Int64
	mTerminated           atomic.Int64
	mSuccessions          atomic.Int64
	mDegradedJoins        atomic.Int64
	mEvictedBeforeHorizon atomic.Int64
	mArchived             atomic.Int64
}

// NewStore builds a lifecycle store. The retention/cap values come from the
// parameters file (doc 14 §1.4/§5: full 15 min, stub 24 h, cap 250k); they are
// passed in so this package stays free of a params dependency. A nil clock
// defaults to time.Now (production); tests inject a controllable clock.
func NewStore(clock func() time.Time, fullRetention, stubRetention time.Duration, maxEntries int) *Store {
	if clock == nil {
		clock = time.Now
	}
	if maxEntries <= 0 {
		maxEntries = 1
	}
	return &Store{
		clock:         clock,
		fullRetention: fullRetention,
		stubRetention: stubRetention,
		maxEntries:    maxEntries,
		byCEI:         make(map[string]*InstanceRecord),
		byName:        make(map[string][]*InstanceRecord),
	}
}

// Observe records that an instance exists (an informer Add/Update). It is
// idempotent for a given CEI and never resurrects a dead record. bornAt is the
// object's own creation time. If an ALIVE same-name instance with a different UID
// exists, it is closed as a predecessor (succession) — this both records role
// continuity and guarantees non-overlapping name intervals even if the old
// instance's delete event was missed or arrives out of order.
func (s *Store) Observe(inst InstanceCoords, role CEI, bornAt time.Time, state LifecycleState) (*InstanceRecord, error) {
	cei, err := MintInstance(inst, bornAt)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := cei.Key()
	if r, ok := s.byCEI[key]; ok {
		// Idempotent: never resurrect a dead record. For a live one, advance state
		// and refresh a previously-degraded role. The true role is immutable, so a
		// changed role is a correction — e.g. the ReplicaSet's owner reached cache
		// after the pod was first observed, upgrading ReplicaSet/x to Deployment/y
		// on the next resync. Nodes carry no role (zero CEI), so skip them.
		if r.alive() {
			if state > r.State {
				r.State = state
			}
			if role.Layer != 0 && r.RoleCEI.Key() != role.Key() {
				r.RoleCEI = role
			}
		}
		return r, nil
	}

	nk := nameKey(inst.Kind, inst.Namespace, inst.Name)
	// Find alive same-name predecessors in a READ-ONLY pass first: terminating them
	// below can trigger LRU eviction that mutates this same byName slice, so we must
	// not range it while mutating (that could skip the predecessor and leave two
	// live same-name records — an ambiguous join on the trust-critical path).
	var preds []*InstanceRecord
	for _, r := range s.byName[nk] {
		if r.alive() && r.UID != inst.UID {
			preds = append(preds, r)
		}
	}

	rec := &InstanceRecord{
		CEI: cei, RoleCEI: role, State: state, BornAt: bornAt,
		Kind: inst.Kind, Namespace: inst.Namespace, Name: inst.Name, UID: inst.UID,
		heapIdx: -1,
	}
	s.byCEI[key] = rec
	s.insertByNameLocked(nk, rec)
	s.mDiscovered.Add(1)

	// Now close the predecessor(s) — after the read pass, so eviction is safe.
	var pred *InstanceRecord
	for _, p := range preds {
		s.terminateLocked(p, bornAt) // predecessor died at the successor's birth
		pred = p
	}
	if pred != nil {
		rec.Predecessor = pred.CEI.Key()
		pred.Successor = key
		s.mSuccessions.Add(1)
	}
	return rec, nil
}

// TerminateInstance marks the instance at the given coordinates dead at diedAt.
// Idempotent; a no-op if unknown or already dead.
func (s *Store) TerminateInstance(inst InstanceCoords, diedAt time.Time) {
	cei, err := MintInstance(inst, diedAt)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.byCEI[cei.Key()]; ok {
		s.terminateLocked(r, diedAt)
	}
}

func (s *Store) terminateLocked(r *InstanceRecord, diedAt time.Time) {
	if !r.alive() {
		return
	}
	if diedAt.Before(r.BornAt) {
		diedAt = r.BornAt // no negative lifetimes (clock skew / out-of-order)
	}
	r.DiedAt = diedAt
	r.State = StateTerminated
	heap.Push(&s.dead, r)
	s.mTerminated.Add(1)
	s.enforceCapLocked()
}

func (s *Store) insertByNameLocked(nk string, rec *InstanceRecord) {
	lst := s.byName[nk]
	idx := sort.Search(len(lst), func(i int) bool { return lst[i].BornAt.After(rec.BornAt) })
	lst = append(lst, nil)
	copy(lst[idx+1:], lst[idx:])
	lst[idx] = rec
	s.byName[nk] = lst
}

func (s *Store) evictLocked(r *InstanceRecord) {
	delete(s.byCEI, r.CEI.Key())
	nk := nameKey(r.Kind, r.Namespace, r.Name)
	lst := s.byName[nk]
	for i, x := range lst {
		if x == r {
			s.byName[nk] = append(lst[:i], lst[i+1:]...)
			break
		}
	}
	if len(s.byName[nk]) == 0 {
		delete(s.byName, nk)
	}
}

// enforceCapLocked drops tombstones until the dead set is within the hard cap
// (doc 14 §1.4). The doc names this "LRU eviction"; we evict by OLDEST DEATH,
// which is the correct policy for this store's purpose: late samples arrive within
// a bounded window after death (watermark + pipeline delay), so the oldest-dead
// tombstones are the ones LEAST likely to still be needed — evicting them first
// sheds the least useful entries. An eviction of a tombstone still inside its
// retention horizon is a join-risk signal, counted as EvictedBeforeHorizon and
// never silent (the late sample it would have served then honestly quarantines
// rather than mis-joining a same-name successor).
func (s *Store) enforceCapLocked() {
	now := s.clock()
	for s.dead.Len() > s.maxEntries {
		r := heap.Pop(&s.dead).(*InstanceRecord)
		withinHorizon := now.Sub(r.DiedAt) < s.stubRetention
		s.evictLocked(r)
		if withinHorizon {
			s.mEvictedBeforeHorizon.Add(1)
		} else {
			s.mArchived.Add(1)
		}
	}
}

// GC reclaims memory: it evicts tombstones aged past stubRetention (which would be
// appended to the on-disk identity log — a follow-on) and re-enforces the cap.
// Lookup correctness does not depend on GC running; it only reclaims memory.
func (s *Store) GC() {
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.dead.Len() > 0 {
		r := s.dead.items[0]
		if now.Sub(r.DiedAt) < s.stubRetention {
			break
		}
		heap.Pop(&s.dead)
		s.evictLocked(r)
		s.mArchived.Add(1)
	}
	s.enforceCapLocked()
}

// tierOf classifies a record at instant now (caller holds at least RLock).
func (s *Store) tierOf(r *InstanceRecord, now time.Time) Tier {
	if r.alive() {
		return TierAlive
	}
	age := now.Sub(r.DiedAt)
	switch {
	case age < s.fullRetention:
		return TierFull
	case age < s.stubRetention:
		return TierStub
	default:
		return TierExpired
	}
}

// PodUID returns the UID of the pod that was (namespace, name) at instant at.
// Implements Lookup. Late samples within the in-memory horizon resolve to the
// correct now-dead instance; ones on a stub-tier tombstone are counted as degraded.
func (s *Store) PodUID(namespace, name string, at time.Time) (string, bool) {
	return s.lookup("Pod", namespace, name, at)
}

// NodeUID returns the UID of the node that was `name` at instant at. Implements Lookup.
func (s *Store) NodeUID(name string, at time.Time) (string, bool) {
	return s.lookup("Node", "", name, at)
}

// PVCUID returns the UID of the PersistentVolumeClaim that was (namespace, name) at
// instant at. Implements Lookup. PVCs are Observe()d into the store like pods/nodes
// (the Watcher's PVC informer), so the same time-aware succession disambiguation
// applies — a recreated claim of the same name resolves to the correct generation.
func (s *Store) PVCUID(namespace, name string, at time.Time) (string, bool) {
	return s.lookup("PersistentVolumeClaim", namespace, name, at)
}

// PDBUID returns the UID of the PodDisruptionBudget that was (namespace, name) at instant
// at (docs/33 build 2). Implements Lookup. PDBs are Observe()d into the store like PVCs
// (the Watcher's PDB informer), so the same time-aware succession disambiguation applies.
func (s *Store) PDBUID(namespace, name string, at time.Time) (string, bool) {
	return s.lookup("PodDisruptionBudget", namespace, name, at)
}

// StatefulSetUID returns the UID of the StatefulSet that was (namespace, name) at instant at
// (docs/33 closure 1, v0.18.0). Implements Lookup. StatefulSets are Observe()d into the store
// like PVCs/PDBs (the Watcher's apps/v1 informer), so the same time-aware succession
// disambiguation applies — a recreated workload of the same name resolves to the right generation.
func (s *Store) StatefulSetUID(namespace, name string, at time.Time) (string, bool) {
	return s.lookup("StatefulSet", namespace, name, at)
}

func (s *Store) lookup(kind, namespace, name string, at time.Time) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := s.clock()
	lst := s.byName[nameKey(kind, namespace, name)]
	// Records are ordered by BornAt and their intervals are non-overlapping; the
	// newest covering record is the unique answer. Iterate newest-first.
	for i := len(lst) - 1; i >= 0; i-- {
		r := lst[i]
		if !r.coversInstant(at) {
			continue
		}
		switch s.tierOf(r, now) {
		case TierExpired:
			// Past the in-memory horizon: treated as absent (quarantine upstream),
			// even before GC physically reclaims it.
			return "", false
		case TierStub:
			s.mDegradedJoins.Add(1)
		}
		return r.UID, true
	}
	return "", false
}

// Get returns a SNAPSHOT copy of the record for a CEI key, if present. A copy
// (not the live pointer) is returned so callers can read it without racing the
// store's own locked mutations.
func (s *Store) Get(ceiKey string) (InstanceRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.byCEI[ceiKey]
	if !ok {
		return InstanceRecord{}, false
	}
	return *r, true
}

// ActiveInstances returns snapshot copies of every alive instance record (DiedAt
// zero), for surfacing the live, correctly-joined entity inventory (doc 03 §6 —
// "prerequisite zero, observable"). Like Get, it returns value copies so callers
// read them without holding the store lock while the store keeps mutating its own
// pointers. Order is unspecified; the caller sorts for display.
func (s *Store) ActiveInstances() []InstanceRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]InstanceRecord, 0, len(s.byCEI)-s.dead.Len())
	for _, r := range s.byCEI {
		if r.alive() {
			out = append(out, *r)
		}
	}
	return out
}

// Metrics returns a snapshot of the store's published health signals.
func (s *Store) Metrics() Metrics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := s.clock()
	var full, stub int
	for _, r := range s.dead.items {
		switch s.tierOf(r, now) {
		case TierFull:
			full++
		case TierStub:
			stub++
		}
	}
	return Metrics{
		Active:               len(s.byCEI) - s.dead.Len(),
		FullTombstones:       full,
		StubTombstones:       stub,
		Discovered:           s.mDiscovered.Load(),
		Terminated:           s.mTerminated.Load(),
		Successions:          s.mSuccessions.Load(),
		DegradedJoins:        s.mDegradedJoins.Load(),
		EvictedBeforeHorizon: s.mEvictedBeforeHorizon.Load(),
		Archived:             s.mArchived.Load(),
	}
}

// deadHeap is a min-heap of dead records ordered by DiedAt (oldest at the root),
// so eviction (cap pressure and GC) always sheds the oldest tombstone first.
type deadHeap struct{ items []*InstanceRecord }

func (h deadHeap) Len() int           { return len(h.items) }
func (h deadHeap) Less(i, j int) bool { return h.items[i].DiedAt.Before(h.items[j].DiedAt) }
func (h deadHeap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.items[i].heapIdx = i
	h.items[j].heapIdx = j
}

func (h *deadHeap) Push(x any) {
	r := x.(*InstanceRecord)
	r.heapIdx = len(h.items)
	h.items = append(h.items, r)
}

func (h *deadHeap) Pop() any {
	n := len(h.items)
	r := h.items[n-1]
	h.items[n-1] = nil
	h.items = h.items[:n-1]
	r.heapIdx = -1
	return r
}
