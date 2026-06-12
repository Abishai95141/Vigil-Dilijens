// Tier-B budgeting (doc 06 §3.3 / M5): forecasting is deliberately scarce —
// the number of clock invocations per cycle is a configured ceiling, candidates
// are ranked within it, and everything eligible-but-unbudgeted is PUBLISHED,
// never silently omitted. Cost is bounded by construction, not by hope.
//
// v1 ranking policy (stated, pre-06-M4): precursor-bearing targets first (only
// a precursor's crossing carries early-warning meaning, doc 09 §3.2), then
// canonical (CEIKey, Metric) order. The criticality/role weighting of 06 M4
// replaces the tiebreak when it lands; the policy is deliberately simple and
// auditable until then.
package selection

import (
	"sort"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/forecast"
)

// TierBRecord is one ranked Tier-B candidate — the audit surface for "why is
// this forecast and that not" (doc 06 §3.5, MEASURED facts about attention).
type TierBRecord struct {
	Target   forecast.Target
	Rank     int    // 1-based rank under the v1 policy
	Budgeted bool   // within the invocation ceiling this cycle
	Reason   string // precursor | eligible
}

// TierBSelection is the budgeted watch list plus the published remainder.
type TierBSelection struct {
	Budgeted   []TierBRecord
	Unbudgeted []TierBRecord // eligible-but-unbudgeted — visible, never silent (06 §6)
	Ceiling    int
}

// SelectTierB ranks the eligible targets and applies the invocation ceiling.
// Deterministic: same targets + same ceiling ⇒ same selection.
func SelectTierB(targets []forecast.Target, ceiling int) TierBSelection {
	ranked := make([]TierBRecord, 0, len(targets))
	for _, t := range targets {
		reason := "eligible"
		if t.Precursor() {
			reason = "precursor"
		}
		ranked = append(ranked, TierBRecord{Target: t, Reason: reason})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		pi, pj := ranked[i].Target.Precursor(), ranked[j].Target.Precursor()
		if pi != pj {
			return pi // precursors first
		}
		if ranked[i].Target.CEIKey != ranked[j].Target.CEIKey {
			return ranked[i].Target.CEIKey < ranked[j].Target.CEIKey
		}
		return ranked[i].Target.Metric < ranked[j].Target.Metric
	})
	sel := TierBSelection{Ceiling: ceiling}
	for i := range ranked {
		ranked[i].Rank = i + 1
		ranked[i].Budgeted = ceiling <= 0 || i < ceiling
		if ranked[i].Budgeted {
			sel.Budgeted = append(sel.Budgeted, ranked[i])
		} else {
			sel.Unbudgeted = append(sel.Unbudgeted, ranked[i])
		}
	}
	return sel
}

// BudgetedTargets unwraps the budgeted records for the runner.
func BudgetedTargets(sel TierBSelection) []forecast.Target {
	out := make([]forecast.Target, 0, len(sel.Budgeted))
	for _, r := range sel.Budgeted {
		out = append(out, r.Target)
	}
	return out
}
