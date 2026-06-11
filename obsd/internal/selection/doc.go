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
// M1 scope (implemented): gates 1–2 + reason codes + Tier A.
//
//	Gate 1 — coverage: only entities with at least one evaluable bound variable
//	  (bound + resolved bar + validation not failed — exactly the fingerprint-
//	  eligible set, doc 05 §3.3) are candidates. Exclusion is a visible fact.
//	Gate 2 — phenomenon participation: an entity earns attention iff its bound
//	  variables back member signals of at least one phenomenon, read straight
//	  from the graph (binding rule -> signal -> participates_in edges).
//
// Tier B exists structurally but stays EMPTY until Phase 2 (09's signal funnel
// + the budget). Neighbourhood expansion (M2), criticality weighting and
// operator scope (M4), and re-selection diffs (M6) are later milestones —
// absent, stated.
//
// Determinism: TierASet is a pure function of (bindings, graph) — no inventory,
// no clock — because detection consumes it and replay (05 M5) must reproduce it
// exactly from a bundle. The full Select records (including the none-list over
// the live inventory) are display/audit surface, NOT inputs to the
// deterministic path.
//
// Do NOT: manufacture meaning (semantics live in the graph), silently drop an
// important-but-unselected entity — publish the none-list with reasons — or let
// Tier B gate Tier A (doc 01 non-gating).
package selection
