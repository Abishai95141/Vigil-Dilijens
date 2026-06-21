// discovery.go — the OFFLINE causal-discovery ingestion lane (doc 29 §B). The Python harness
// (harness/causal-discovery/) prunes the assoc lane's lag-0 associations with a fixed,
// DECLARED-lag enrichment + PCMCI+ conditional-independence pruning, and emits a ranked JSON
// shortlist of candidate links. This file turns that shortlist into DIRECTION-FREE
// causal-hypothesis candidates — exactly like the C2/C3 co-onset producer above: the PCMCI
// direction is carried ONLY as a PROJECTED hint a named operator authors, never as a cause.
//
// CHARTER (same as cohypothesis.go): the candidate is firewalled (candidates.db), off-digest,
// never read by the deterministic path; the Relation is "co-occurrence" (direction-free); the
// payload holds only MEASURED magnitudes + PROJECTED hints, never an asserted cause.
package cohypothesis

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
)

// DiscoveredLink is one ranked candidate edge from the offline harness. `from`/`to` carry
// PCMCI's directed hint; it is staged DIRECTION-FREE (the operator authors the arrow).
type DiscoveredLink struct {
	From            string  `json:"from"`
	To              string  `json:"to"`
	LagSeconds      int     `json:"lag_seconds"`
	LagBins         int     `json:"lag_bins"`
	Coefficient     float64 `json:"coefficient"`
	Contemporaneous bool    `json:"contemporaneous"`
	LeadlagHintBins int     `json:"leadlag_hint_bins"`
}

// Shortlist is the harness output file. Only `shortlist` is load-bearing here; the rest is
// provenance the harness records for the human.
type Shortlist struct {
	Method    string           `json:"method"`
	Shortlist []DiscoveredLink `json:"shortlist"`
}

// ParseShortlist decodes the harness JSON shortlist. An empty/blank file yields zero links
// (not an error); malformed JSON is an error the caller logs non-fatally.
func ParseShortlist(data []byte) (Shortlist, error) {
	var s Shortlist
	if strings.TrimSpace(string(data)) == "" {
		return s, nil
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, err
	}
	return s, nil
}

// DiscoveredCandidates turns the shortlist into DIRECTION-FREE causal-hypothesis candidates,
// mirroring the C3 co-onset producer: Subject is the SORTED pair "a ~ b", Relation is
// "co-occurrence", the Evidence is STABLE per pair (so the content id IS the pair and
// re-running the harness updates in place, never floods), and PCMCI's direction + lag are
// PROJECTED hints in the payload (excluded from the content id). NO direction is asserted.
// Keeps the strongest maxOut leads (|coefficient|, then subject for determinism); maxOut ≤ 0
// means no cap. Pure + deterministic.
func DiscoveredCandidates(links []DiscoveredLink, graphVersion string, maxOut int) []candidate.Candidate {
	type scored struct {
		c    candidate.Candidate
		coef float64
	}
	var cand []scored
	for _, l := range links {
		from, to := strings.TrimSpace(l.From), strings.TrimSpace(l.To)
		if from == "" || to == "" || from == to {
			continue
		}
		// sort the pair so the subject + content id are stable and DIRECTION-FREE
		a, b := from, to
		if b < a {
			a, b = to, from
		}
		// PCMCI's directed hint mapped onto the sorted (a,b) slots — a PROJECTED hint only
		hint := "a-to-b"
		if from == b {
			hint = "b-to-a"
		}
		cand = append(cand, scored{coef: l.Coefficient, c: candidate.Candidate{
			Kind:     candidate.KindCausalHypothesis,
			Relation: "co-occurrence",
			Subject:  a + " ~ " + b,
			Payload: map[string]any{
				"a": a, "b": b,
				"coefficient":     round3(l.Coefficient), // |Pearson r| of the surviving link (excluded from id)
				"deltaSeconds":    int64(l.LagSeconds),   // lead-lag in seconds (excluded from id)
				"lagBins":         int64(l.LagBins),
				"leadlagHintBins": int64(l.LeadlagHintBins),
				"contemporaneous": l.Contemporaneous,
				"directionHint":   hint, // PROJECTED — the operator authors the real direction
			},
			// STABLE per-pair evidence ⇒ the content id is the pair (re-runs update in place).
			Evidence: []candidate.EvidenceRef{
				{Kind: "associated-with", Ref: "correlated", Detail: "MEASURED association (undirected, never causal)"},
				{Kind: "conditional-independence", Ref: "pcmci-survived", Detail: "survived conditioning on the rest of the panel — not explained away by a common driver or a transitive path"},
				{Kind: "co-occurrence", Ref: "lagged-co-movement", Detail: "co-moves at a measured lag; the lead-lag and direction are PROJECTED hints a human authors, not a cause"},
			},
			Lineage: candidate.Lineage{
				Source: "offline-causal-discovery", Method: "pcmci-parcorr", GraphVersion: graphVersion,
				Inputs: []string{a, b},
			},
		}})
	}
	sort.Slice(cand, func(i, j int) bool {
		ai, aj := math.Abs(cand[i].coef), math.Abs(cand[j].coef)
		if ai != aj {
			return ai > aj
		}
		return cand[i].c.Subject < cand[j].c.Subject
	})
	if maxOut > 0 && len(cand) > maxOut {
		cand = cand[:maxOut]
	}
	out := make([]candidate.Candidate, len(cand))
	for i := range cand {
		out[i] = cand[i].c
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Subject < out[j].Subject })
	return out
}

// StageDiscovered parses the harness shortlist bytes and stages each DIRECTION-FREE candidate
// into the firewalled store, returning the count staged. now is injected (no time.Now here).
func StageDiscovered(s *candidate.Store, now time.Time, data []byte, graphVersion string, maxOut int) (int, error) {
	sl, err := ParseShortlist(data)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range DiscoveredCandidates(sl.Shortlist, graphVersion, maxOut) {
		if _, err := s.Put(now, c); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
