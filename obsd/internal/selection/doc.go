// Package selection is the attention allocator: it decides which entities are
// monitored, with which neighbourhoods, at which intensity — driven by the graph,
// with cost bounded by construction.
//
// Owning doc: 06-monitoring-selection-engine.md.
//
// NOTE on the name: the blueprint refers to this component as "select"
// (techstack §4). The package is named `selection` because `select` is a reserved
// Go keyword and cannot be a package identifier. This is the one intentional,
// documented deviation between the doc's import path and the code.
//
// The funnel (doc 06 §3.1): coverage gate -> phenomenon participation -> forecast
// eligibility (Tier-B narrowing) -> criticality/role weighting -> operator scope.
//
// Tiers (doc 06 §3.3):
//   - A (detection): three-primitive checks; scales to the whole eligible cluster.
//   - B (forecasting): clock inference; deliberately scarce and BUDGETED
//     (tier_b_budget_per_cycle, doc 14 §5).
//   - none: out-of-scope/coverage-failing, listed with reasons.
//
// Every selection record is a MEASURED fact about the system's own attention, with
// a reason code attached — the operator can always answer "why is this watched."
//
// Do NOT: manufacture meaning (semantics live in the graph), or silently drop an
// important-but-unselected entity — publish the none-list with reasons.
package selection
