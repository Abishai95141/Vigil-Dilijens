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
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/assoc"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/onset"
)

// leadLagKey is the lookup key for a pair's lead-lag witness: the SORTED pair (the same
// order Hypothesize sorts a,b), so the caller and the producer agree on which series is "a"
// (a positive peak lag means a led b).
func leadLagKey(a, b string) string {
	if b < a {
		a, b = b, a
	}
	return a + "\x00" + b
}

// CoupledPair is one MEASURED association (an associated-with edge) between two series. It is
// the only coupling the producer trusts; a causal direction is NEVER inferred from it. A and
// B are series identifiers (the hot-store key "CEI|metric").
type CoupledPair struct {
	A, B        string
	Coefficient float64 // Pearson r of the association (a MEASURED magnitude, not a learned weight)
}

// Hypothesize stages a direction-free co-occurrence candidate for every CROSS-WORKLOAD coupled
// pair whose BOTH endpoints registered an onset within `window` of each other (using each
// series' most recent onset). To stay reviewable on a busy cluster — where hundreds of series
// step together and many infra metrics co-move — it keeps only the strongest `maxOut`
// (highest |coefficient|, then tightest co-onset); maxOut ≤ 0 means no cap. Pure +
// deterministic: same inputs ⇒ identical candidates (the content id dedups re-staging).
// leadlag carries the OPTIONAL rigorous lead-lag witness per sorted pair (docs/31 §5), keyed
// by leadLagKey(a,b). A present entry attaches sign-carrying lead-lag EVIDENCE to the pair's
// payload (never a direction); an absent entry is honest silence (no significant lead). It is
// payload-only — excluded from the content id — so a witness appearing/changing across cycles
// never re-mints or floods the candidate. nil ⇒ the lane runs without the lead-lag enrichment.
func Hypothesize(onsets []onset.Onset, pairs []CoupledPair, leadlag map[string]assoc.LeadLagWitness, window time.Duration, graphVersion string, maxOut int) []candidate.Candidate {
	latest := map[string]onset.Onset{}
	for _, o := range onsets {
		k := o.EntityCEI + "|" + o.Metric
		if e, ok := latest[k]; !ok || o.At.After(e.At) {
			latest[k] = o
		}
	}
	type scored struct {
		c     candidate.Candidate
		coef  float64
		delta int64
	}
	var cand []scored
	for _, p := range pairs {
		oa, okA := latest[p.A]
		ob, okB := latest[p.B]
		if !okA || !okB {
			continue // one side did not step ⇒ no co-onset
		}
		if absDur(oa.At.Sub(ob.At)) > window {
			continue // stepped too far apart ⇒ not a co-onset
		}
		if entityOf(p.A) == entityOf(p.B) || samePod(p.A, p.B) {
			// same entity, OR the SAME physical pod seen as both a Pod CEI and a Container CEI
			// (shared pod UID) — two facets of one workload, not a CROSS-workload lead.
			continue
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
		payload := map[string]any{
			"a": a, "aOnsetTs": oA.At.UTC().Format(time.RFC3339Nano), "aDirection": oA.Direction, "aStepZ": oA.StepZ,
			"b": b, "bOnsetTs": oB.At.UTC().Format(time.RFC3339Nano), "bDirection": oB.Direction, "bStepZ": oB.StepZ,
			"coefficient":   round3(p.Coefficient), // the lag-0 LEVEL correlation (distinct from lagPeakRDetrended)
			"deltaSeconds":  deltaSec,
			"observedFirst": first, // MEASURED observed order — explicitly NOT a cause
			"windowSeconds": int64(window / time.Second),
		}
		// docs/31 §5: attach the rigorous lead-lag witness when one survived (significant,
		// nonzero, detrended). It is sign-carrying EVIDENCE (positive lag = a appeared to lead
		// b), NEVER a direction. lagConsistentWithOnset cross-checks the peak-lag sign against
		// the INDEPENDENT onset-order witness — two witnesses agreeing is a stronger lead to
		// investigate; disagreeing downgrades to "order unclear" (surfaced, not asserted). All
		// lead-lag fields are payload-only (excluded from the content id).
		if w, ok := leadlag[leadLagKey(a, b)]; ok {
			onsetSign := 0
			switch first {
			case a:
				onsetSign = 1
			case b:
				onsetSign = -1
			}
			lagSign := 0
			switch {
			case w.LagPeakBins > 0:
				lagSign = 1
			case w.LagPeakBins < 0:
				lagSign = -1
			}
			payload["lagPeakSeconds"] = w.LagPeakSeconds
			payload["lagPeakRDetrended"] = round3(w.LagPeakRDetrended)
			payload["lagP"] = round3(w.LagP)
			payload["effectiveN"] = int64(w.EffectiveN)
			payload["lagConsistentWithOnset"] = onsetSign != 0 && onsetSign == lagSign
		}
		cand = append(cand, scored{coef: p.Coefficient, delta: deltaSec, c: candidate.Candidate{
			Kind:     candidate.KindCausalHypothesis,
			Relation: "co-occurrence",
			Subject:  a + " ~ " + b,
			Payload:  payload,
			// Evidence is STABLE per pair (no per-cycle timestamps/coefficient in the refs) so the
			// content id is the PAIR — re-staging the same pair updates in place, never floods. The
			// changing values (onset times, r, delta) live in the payload + the surfaced row.
			Evidence: []candidate.EvidenceRef{
				{Kind: "measured-changepoint", Ref: a, Detail: "stepped " + oA.Direction},
				{Kind: "measured-changepoint", Ref: b, Detail: "stepped " + oB.Direction},
				{Kind: "associated-with", Ref: "correlated", Detail: "MEASURED correlation (undirected, never causal)"},
				{Kind: "co-occurrence", Ref: "co-stepped", Detail: "both stepped within the co-onset window; the observed order is not a cause"},
			},
			Lineage: candidate.Lineage{
				Source: "co-onset", Method: "associated-co-onset", GraphVersion: graphVersion,
				Inputs: []string{a, b},
			},
		}})
	}
	// Keep only the strongest leads (highest |coef|, then tightest co-onset, then subject) so
	// the firewalled store stays reviewable on a busy cluster; ties broken by subject for
	// determinism. maxOut ≤ 0 ⇒ no cap.
	sort.Slice(cand, func(i, j int) bool {
		ai, aj := math.Abs(cand[i].coef), math.Abs(cand[j].coef)
		if ai != aj {
			return ai > aj
		}
		if cand[i].delta != cand[j].delta {
			return cand[i].delta < cand[j].delta
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

// HypothesizeAndStage runs Hypothesize and stages each candidate into the firewalled store,
// returning the count staged. now is injected (no time.Now in the package).
func HypothesizeAndStage(s *candidate.Store, now time.Time, onsets []onset.Onset, pairs []CoupledPair, leadlag map[string]assoc.LeadLagWitness, window time.Duration, graphVersion string, maxOut int) (int, error) {
	n := 0
	for _, c := range Hypothesize(onsets, pairs, leadlag, window, graphVersion, maxOut) {
		if _, err := s.Put(now, c); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// entityOf returns the entity (CEI) of a "CEI|metric" stream key: the first 6 "|"-separated
// fields (layer|cluster|ns|kind|name|uid). Two streams of the SAME entity (e.g. two memory
// facets of one container) are NOT a cross-workload lead, so the producer skips them.
func entityOf(streamKey string) string {
	n := 0
	for i := 0; i < len(streamKey); i++ {
		if streamKey[i] == '|' {
			n++
			if n == 6 {
				return streamKey[:i]
			}
		}
	}
	return streamKey
}

// samePod reports whether two "CEI|metric" stream keys belong to the same physical pod. The
// CEI's uid field (index 5 of "i|cluster|ns|Kind|name|uid|metric…") is the pod UID, or
// "podUID/container" for a Container CEI — so the same pod appears as both a Pod CEI and a
// Container CEI with the SAME pod UID; those are not a cross-workload lead.
func samePod(a, b string) bool {
	ua, ub := podUID(a), podUID(b)
	return ua != "" && ua == ub
}

func podUID(streamKey string) string {
	parts := strings.Split(streamKey, "|")
	if len(parts) < 6 {
		return ""
	}
	uid := parts[5]
	if s := strings.IndexByte(uid, '/'); s >= 0 {
		uid = uid[:s] // strip the "/container" suffix on a Container CEI
	}
	return uid
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
