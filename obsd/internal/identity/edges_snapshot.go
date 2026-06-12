package identity

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Topology snapshots (doc 07 §3.7): "same readings + same graph version + same
// TOPOLOGY SNAPSHOT ⇒ same matches". The snapshot is the replay-bundle form of
// the edge store — every assertion with its full validity interval, in canonical
// order — so a replayed tick traverses EXACTLY the topology the live tick saw,
// through the same Traverse/Neighbours code path. Capture evaluates against the
// snapshot it records (never the live store), so recorded == evaluated by
// construction even while informers keep mutating the live store.

// EdgeSnap is one edge assertion in bundle form. From/To are CEI keys (the only
// coordinates traversal consults); timestamps carry the validity interval.
type EdgeSnap struct {
	Type          string    `json:"type"`
	From          string    `json:"from"`
	To            string    `json:"to"`
	AssertedAt    time.Time `json:"asserted_at"`
	LastConfirmed time.Time `json:"last_confirmed"`
	RetractedAt   time.Time `json:"retracted_at,omitempty"`
}

// Snapshot returns every resident assertion (live, suspect, and retracted-but-
// within-horizon — Traverse decides per window, the snapshot never pre-filters),
// canonically sorted by (type, from, to). Deterministic given store state.
func (s *EdgeStore) Snapshot() []EdgeSnap {
	s.mu.RLock()
	out := make([]EdgeSnap, 0, len(s.edges))
	for _, e := range s.edges {
		out = append(out, EdgeSnap{
			Type: string(e.Type), From: e.From.Key(), To: e.To.Key(),
			AssertedAt: e.AssertedAt.UTC(), LastConfirmed: e.LastConfirmed.UTC(),
			RetractedAt: retractedUTC(e.RetractedAt),
		})
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.From != b.From {
			return a.From < b.From
		}
		return a.To < b.To
	})
	return out
}

func retractedUTC(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{} // keep the zero value zero (omitempty + "live" semantics)
	}
	return t.UTC()
}

// NewEdgeStoreFromSnapshot rebuilds a traversable store from a snapshot under
// the given staleness budgets (pinned in the bundle manifest — suspicion is a
// budget-relative judgement and must replay under the CAPTURE's budgets, never
// the local parameter file's). The rebuilt store is read-side only by
// convention: GC/Assert work but replay has no reason to call them.
func NewEdgeStoreFromSnapshot(snap []EdgeSnap, budgets map[EdgeType]time.Duration) (*EdgeStore, error) {
	s := NewEdgeStore(nil, budgets, 24*time.Hour)
	for i, es := range snap {
		from, err := ParseKey(es.From)
		if err != nil {
			return nil, fmt.Errorf("topology snapshot row %d: from: %w", i, err)
		}
		to, err := ParseKey(es.To)
		if err != nil {
			return nil, fmt.Errorf("topology snapshot row %d: to: %w", i, err)
		}
		if es.AssertedAt.IsZero() {
			return nil, fmt.Errorf("topology snapshot row %d (%s %s->%s): zero asserted_at", i, es.Type, es.From, es.To)
		}
		e := &EdgeAssertion{
			Type: EdgeType(es.Type), From: from, To: to,
			AssertedAt: es.AssertedAt, LastConfirmed: es.LastConfirmed, RetractedAt: es.RetractedAt,
		}
		k := edgeKey(e.Type, es.From, es.To)
		if _, dup := s.edges[k]; dup {
			return nil, fmt.Errorf("topology snapshot row %d: duplicate edge %s %s->%s", i, es.Type, es.From, es.To)
		}
		s.edges[k] = e
		s.byFrom[es.From] = append(s.byFrom[es.From], e)
		s.byTo[es.To] = append(s.byTo[es.To], e)
	}
	return s, nil
}

// ParseKey reconstructs a CEI from its Key() form. Mint validation guarantees no
// coordinate contains the separator, so the split is exact. Only the layers that
// appear in topology edges are accepted.
func ParseKey(key string) (CEI, error) {
	parts := strings.Split(key, sep)
	switch {
	case len(parts) == 6 && parts[0] == "i":
		return CEI{
			Layer: LayerInstance, Cluster: parts[1], Namespace: parts[2],
			Kind: parts[3], Name: parts[4], UID: parts[5],
		}, nil
	case len(parts) == 5 && parts[0] == "r":
		return CEI{
			Layer: LayerRole, Cluster: parts[1], Namespace: parts[2],
			Kind: parts[3], RoleKey: parts[4],
		}, nil
	default:
		return CEI{}, fmt.Errorf("unparseable CEI key %q", key)
	}
}
