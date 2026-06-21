package candidate

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

// StrayObservation is a metric the identity normalizer QUARANTINED — it carries
// labels but could not be joined to a CEI (doc 03 quarantine-never-guess). The ER
// fallback (doc 20 §2.4) proposes what it might be, never guesses it.
type StrayObservation struct {
	Family       string
	Metric       string
	Labels       map[string]string
	Node         string
	Reason       string // the quarantine reason (e.g. "unknown-exporter-family")
	StreamRef    string // a stable reference to the MEASURED series (evidence)
	GraphVersion string
}

// EntityRef is one active, correctly-joined entity the ER matches a stray against —
// its stable key + the identifying coordinates. A plain struct so the candidate
// package never imports identity (the surfacing/firewall decoupling); the caller maps
// identity.InstanceRecord -> EntityRef.
type EntityRef struct {
	Key       string
	Kind      string
	Namespace string
	Name      string
	UID       string
}

// coords returns the entity's non-empty identifying coordinates, by dimension.
func (e EntityRef) coords() []dimVal {
	out := make([]dimVal, 0, 3)
	for _, d := range []dimVal{
		{"namespace", e.Namespace},
		{"name", e.Name},
		{"uid", e.UID},
	} {
		if d.val != "" {
			out = append(out, d)
		}
	}
	return out
}

type dimVal struct{ dim, val string }

type agreedDim struct {
	dim   string // the entity coordinate dimension that matched
	val   string // the shared value
	label string // the stray label key carrying that value
}

// Resolve is the deterministic stray-metric entity resolution (doc 20 §2.4). It
// returns a PROVISIONAL candidate node for the stray metric, plus — when the discrete
// coordinate evidence is identifying — `associated-with` candidate EDGES to the
// best-matching entities.
//
// The evidence is a SET of shared coordinates (namespace/name/uid), never a score:
// there is no Fellegi-Sunter m/u weight and no tunable threshold (those are learned
// quantities, charter-forbidden). The only ranking is by COUNT of shared coordinates
// (argmax, ties surfaced together, sorted by key). The IDENTIFICATION FLOOR is
// structural: ≥2 shared coordinates legitimize an entity link (one shared label is
// co-occurrence, not identity); below it, the stray surfaces alone, ambiguity stated.
// Pure + deterministic given (stray, inventory).
func Resolve(stray StrayObservation, inventory []EntityRef) []Candidate {
	subj := "stray:" + stray.Metric + "/" + strayKey(stray)
	lineage := Lineage{
		Source: "cei-fallback", Method: "discrete-coordinate-intersection", GraphVersion: stray.GraphVersion,
	}

	type match struct {
		e    EntityRef
		dims []agreedDim
		n    int // identification strength = DISTINCT shared values (see matchStrength)
	}
	var matches []match
	maxN := 0
	for _, e := range inventory {
		dims := agreedCoordinates(stray.Labels, e)
		if len(dims) == 0 {
			continue
		}
		n := matchStrength(dims)
		if n > maxN {
			maxN = n
		}
		matches = append(matches, match{e, dims, n})
	}

	nodeReason := "unmapped metric (" + stray.Reason + ")"
	if maxN < 2 {
		nodeReason += "; no identifying entity overlap (≤1 shared coordinate)"
	}
	node := Candidate{
		Kind:    KindNode,
		Subject: subj,
		Payload: map[string]any{
			"family": stray.Family, "metric": stray.Metric, "node": stray.Node,
			"reason": stray.Reason, "labels": stray.Labels,
		},
		Evidence: []EvidenceRef{{Kind: "measured-series", Ref: stray.StreamRef, Detail: nodeReason}},
		Lineage:  lineage,
	}
	out := []Candidate{node}

	// Identification floor: only ≥2 shared coordinates legitimize an entity link.
	if maxN < 2 {
		return out
	}
	var winners []match
	for _, m := range matches {
		if m.n == maxN {
			winners = append(winners, m)
		}
	}
	sort.Slice(winners, func(i, j int) bool { return winners[i].e.Key < winners[j].e.Key })
	for _, m := range winners {
		ev := make([]EvidenceRef, 0, len(m.dims))
		for _, d := range m.dims {
			ev = append(ev, EvidenceRef{Kind: "coordinate-match", Ref: d.dim + "=" + d.val, Detail: "stray label " + d.label})
		}
		out = append(out, Candidate{
			Kind:     KindEdge,
			Relation: "associated-with",
			Subject:  subj + " ~> " + m.e.Key,
			Payload: map[string]any{
				"strayMetric": stray.Metric, "entityKey": m.e.Key,
				"entityKind": m.e.Kind, "sharedCoordinates": m.n,
			},
			Evidence: ev,
			Lineage:  lineage,
		})
	}
	return out
}

// matchStrength is the identification strength of an entity match: the number of DISTINCT
// shared VALUES among the agreed coordinates. Two entity dimensions that agree on the SAME
// value are ONE identifying fact, not two — e.g. a PVC whose name == its namespace (both
// "erpnext") coincides with a single stray label value "erpnext" and would otherwise score
// 2 coordinates (namespace + name) off one coincidence, spuriously clearing the ≥2-coordinate
// identification floor and minting a false association. Distinct VALUES, not dimension count,
// is what legitimizes an entity link. Deterministic; still a pure count, no learning.
func matchStrength(dims []agreedDim) int {
	seen := make(map[string]struct{}, len(dims))
	for _, d := range dims {
		seen[d.val] = struct{}{}
	}
	return len(seen)
}

// agreedCoordinates returns the entity coordinate dimensions whose value is carried by
// SOME stray label (each dim counted once; deterministic label tiebreak). The match is
// value-equality over the discrete coordinate set — no fuzzy/scored comparison.
func agreedCoordinates(labels map[string]string, e EntityRef) []agreedDim {
	var out []agreedDim
	for _, d := range e.coords() {
		if lk, ok := strayLabelWithValue(labels, d.val); ok {
			out = append(out, agreedDim{dim: d.dim, val: d.val, label: lk})
		}
	}
	return out
}

// strayLabelWithValue finds the lexicographically smallest stray label key whose value
// equals val (deterministic), or ("", false).
func strayLabelWithValue(labels map[string]string, val string) (string, bool) {
	best, found := "", false
	for k, v := range labels {
		if v == val && (!found || k < best) {
			best, found = k, true
		}
	}
	return best, found
}

// strayKey is a short, deterministic fingerprint of a stray observation's identity
// (family + node + sorted labels), so the same stray maps to a stable provisional id.
func strayKey(o StrayObservation) string {
	keys := make([]string, 0, len(o.Labels))
	for k := range o.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(o.Family)
	b.WriteByte('|')
	b.WriteString(o.Node)
	for _, k := range keys {
		b.WriteByte('|')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(o.Labels[k])
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:6])
}

// Stage Puts each candidate (the ER's output) into the store, returning the count
// staged. `now` is injected (the store never reads the wall clock).
func (s *Store) Stage(now time.Time, cands []Candidate) (int, error) {
	n := 0
	for _, c := range cands {
		if _, err := s.Put(now, c); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// ResolveAndStage resolves each stray against the inventory and stages the resulting
// candidates, returning the total staged. This is the testable per-cycle core of the
// P1 resolve loop (doc 20 §2.4); the runtime loop is a thin mapper around it.
func ResolveAndStage(s *Store, now time.Time, strays []StrayObservation, inventory []EntityRef) (int, error) {
	total := 0
	for _, stray := range strays {
		n, err := s.Stage(now, Resolve(stray, inventory))
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}
