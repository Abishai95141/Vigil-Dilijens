// Package cohypothesis is the off-digest producer of DIRECTION-FREE causal hypotheses
// (doc 22 C3): the charter-clean reframe of correlation-plus-precedence root cause. For a
// pair of series that are (a) ASSOCIATED (a MEASURED associated-with edge) and (b) both
// registered a changepoint ONSET (C2) within a small window, it stages a candidate that says
// only "these two coupled series stepped together" — never "A caused B". The observed
// temporal ORDER is carried as MEASURED EVIDENCE (which stepped first), so a human can use it,
// but the candidate asserts NO direction. A named operator authors the causal direction (or
// rejects it) through governance.
//
// This is exactly the capability the competitor's engine has and Vigil's charter forbids it
// to ASSERT — so Vigil surfaces the lead and refuses to draw the arrow. The empirical reason
// is also sound: the competitor's auto-direction was shown (E3b/E4b) to invent edges from
// near-simultaneous onsets and common-cause confounders. Direction is the operator's call.
//
// CHARTER:
//   - The staged candidate is firewalled (candidates.db), never read by the deterministic
//     path (the candidate firewall test enforces it), so it can never feed detection.
//   - Relation is always "co-occurrence" (direction-free). There is NO "cause"/"direction"
//     field anywhere in the payload — only MEASURED facts (onset times, directions, the
//     association coefficient, the observed order).
//   - Off-digest: nothing here touches replay.Digest.
package cohypothesis

import (
	"fmt"
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/onset"
)

// CoupledPair is one MEASURED association (an associated-with edge) between two series. It is
// the only coupling the producer trusts; a causal direction is NEVER inferred from it. A and
// B are series identifiers (the hot-store key "CEI|metric").
type CoupledPair struct {
	A, B        string
	Coefficient float64 // Pearson r of the association (a MEASURED magnitude, not a learned weight)
}

// Hypothesize stages a direction-free co-occurrence candidate for every coupled pair whose
// BOTH endpoints registered an onset within `window` of each other (using each series' most
// recent onset). Pure + deterministic: same onsets + pairs + window + version ⇒ identical
// candidates (the content id dedups re-staging).
func Hypothesize(onsets []onset.Onset, pairs []CoupledPair, window time.Duration, graphVersion string) []candidate.Candidate {
	latest := map[string]onset.Onset{}
	for _, o := range onsets {
		k := o.EntityCEI + "|" + o.Metric
		if e, ok := latest[k]; !ok || o.At.After(e.At) {
			latest[k] = o
		}
	}
	var out []candidate.Candidate
	for _, p := range pairs {
		oa, okA := latest[p.A]
		ob, okB := latest[p.B]
		if !okA || !okB {
			continue // one side did not step ⇒ no co-onset
		}
		if absDur(oa.At.Sub(ob.At)) > window {
			continue // stepped too far apart ⇒ not a co-onset
		}
		// Sort the pair so the subject + content id are stable and DIRECTION-FREE.
		a, oA, b, oB := p.A, oa, p.B, ob
		if b < a {
			a, oA, b, oB = p.B, ob, p.A, oa
		}
		// The observed order is a MEASURED fact, surfaced for the operator — NOT a direction.
		first := "near-simultaneous"
		switch {
		case oA.At.Before(oB.At):
			first = a
		case oB.At.Before(oA.At):
			first = b
		}
		deltaSec := int64(absDur(oA.At.Sub(oB.At)) / time.Second)
		out = append(out, candidate.Candidate{
			Kind:     candidate.KindCausalHypothesis,
			Relation: "co-occurrence",
			Subject:  a + " ~ " + b,
			Payload: map[string]any{
				"a": a, "aOnsetTs": oA.At.UTC().Format(time.RFC3339Nano), "aDirection": oA.Direction, "aStepZ": oA.StepZ,
				"b": b, "bOnsetTs": oB.At.UTC().Format(time.RFC3339Nano), "bDirection": oB.Direction, "bStepZ": oB.StepZ,
				"coefficient":   round3(p.Coefficient),
				"deltaSeconds":  deltaSec,
				"observedFirst": first, // MEASURED observed order — explicitly NOT a cause
				"windowSeconds": int64(window / time.Second),
			},
			Evidence: []candidate.EvidenceRef{
				{Kind: "measured-changepoint", Ref: a + "@" + oA.At.UTC().Format(time.RFC3339Nano), Detail: "stepped " + oA.Direction},
				{Kind: "measured-changepoint", Ref: b + "@" + oB.At.UTC().Format(time.RFC3339Nano), Detail: "stepped " + oB.Direction},
				{Kind: "associated-with", Ref: fmt.Sprintf("r=%.3g", p.Coefficient), Detail: "MEASURED correlation (undirected, never causal)"},
				{Kind: "co-occurrence", Ref: fmt.Sprintf("delta=%ds", deltaSec), Detail: "both stepped within the co-onset window; the observed order is not a cause"},
			},
			Lineage: candidate.Lineage{
				Source: "co-onset", Method: "associated-co-onset", GraphVersion: graphVersion,
				Inputs: []string{a, b},
			},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Subject < out[j].Subject })
	return out
}

// HypothesizeAndStage runs Hypothesize and stages each candidate into the firewalled store,
// returning the count staged. now is injected (no time.Now in the package).
func HypothesizeAndStage(s *candidate.Store, now time.Time, onsets []onset.Onset, pairs []CoupledPair, window time.Duration, graphVersion string) (int, error) {
	n := 0
	for _, c := range Hypothesize(onsets, pairs, window, graphVersion) {
		if _, err := s.Put(now, c); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func round3(v float64) float64 {
	return float64(int64(v*1000+sign(v)*0.5)) / 1000
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}
