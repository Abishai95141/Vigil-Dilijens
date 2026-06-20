package candidate

import "strings"

// Support is a candidate's DETERMINISTIC, MEASURED support (doc 21 §4): a STRUCT OF INTEGER
// COUNTS, never a single fitted number and never a model confidence. The review UI ranks
// candidates by the lexicographic tuple of these counts — exactly the contract in this
// package's EvidenceRef doc ("Ranking, where needed, is a lexicographic function of the
// agreed-evidence set, never a number"). There is no learned weight, no coefficient, and no
// sum-that-reads-like-a-score anywhere. Every field is a count of facts already in the
// store, so recomputing Support on identical inputs is byte-identical (determinism gate).
//
// The agent's own rationale (a PROJECTED model utterance) contributes ZERO to Support — only
// agreed MEASURED facts count.
type Support struct {
	EvidenceCount    int   `json:"evidenceCount"`    // # discrete grounded MEASURED refs the candidate cites
	CaptureSample    int   `json:"captureSample"`    // equiv_group: # OTHER current strays the proposed regex also absorbs
	Recurrence       int   `json:"recurrence"`       // # times this gap deterministically re-surfaced (gap_state attempts; 0 if none)
	DistinctEntities int   `json:"distinctEntities"` // # distinct entity CEIs cited in the evidence
	AgeSeconds       int64 `json:"ageSeconds"`       // updated−created; the lexicographic tiebreaker (newer ranks higher)
}

// Score computes a candidate's Support. `recurrence` is the gap-state attempt count for this
// candidate's gap (0 when no gap state exists — Phase 3 step 3 supplies it). Pure: identical
// inputs always yield an identical Support.
func Score(c Candidate, recurrence int) Support {
	s := Support{EvidenceCount: len(c.Evidence), Recurrence: max0(recurrence)}
	// Distinct entity CEIs the candidate is grounded on (the "entity:" evidence refs minted
	// at agent grounding). A malformed ref simply isn't counted — never a crash.
	ents := make(map[string]bool)
	for _, e := range c.Evidence {
		if strings.HasPrefix(e.Ref, "entity:") {
			ents[e.Ref] = true
		}
	}
	s.DistinctEntities = len(ents)
	// equiv_group capture sample (the deterministic count EquivGroupSupport produces).
	if c.Kind == KindEquivGroup {
		if cs, ok := c.Payload["capture_sample"].([]any); ok {
			s.CaptureSample = len(cs)
		}
	}
	if !c.UpdatedAt.IsZero() && !c.CreatedAt.IsZero() {
		if d := int64(c.UpdatedAt.Sub(c.CreatedAt).Seconds()); d > 0 {
			s.AgeSeconds = d
		}
	}
	return s
}

// Less reports whether s ranks BELOW o (weaker support) under the lexicographic order the
// review UI sorts by (strongest first): more evidence, then a larger capture sample, then
// more recurrence, then more distinct entities, and finally NEWER (smaller age) as the last
// tiebreaker. Pure comparison of counts — no weighting.
func (s Support) Less(o Support) bool {
	switch {
	case s.EvidenceCount != o.EvidenceCount:
		return s.EvidenceCount < o.EvidenceCount
	case s.CaptureSample != o.CaptureSample:
		return s.CaptureSample < o.CaptureSample
	case s.Recurrence != o.Recurrence:
		return s.Recurrence < o.Recurrence
	case s.DistinctEntities != o.DistinctEntities:
		return s.DistinctEntities < o.DistinctEntities
	default:
		return s.AgeSeconds > o.AgeSeconds // newer (smaller age) ranks higher
	}
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
