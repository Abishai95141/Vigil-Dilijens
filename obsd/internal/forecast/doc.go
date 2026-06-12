// Package forecast is the forecasting layer's deterministic half (doc 09):
// the signal-level eligibility funnel (§3.2), the per-target pipeline minus
// the model (§3.3 — fetch, dynamics guard, projection, guardrails), and the
// early-warning candidate type (§3.9). The model itself lives behind the
// clock contract (internal/clock ↔ clockd); this package decides WHAT runs,
// projects the clock's trajectory against the graph's resolved bar, and
// enforces the emission guardrails — silence is the default output (§3.6).
//
// Owning doc: 09-forecasting-layer.md. The entity-level Tier-B budget is
// selection's (doc 06 M5, internal/selection/tierb.go); this package owns the
// signal level and the pipeline.
//
// Epistemic discipline (doc 01):
//   - Output is PROJECTED-class with AUTHORED references attached (precursor
//     phenomenon ids, bar provenance), never fused — no generated "why" text.
//   - The two permitted graph→model flows are target selection (here) and
//     footprint subtraction (Phase 3, not yet built). The clock's input is a
//     bare float series; nothing semantic crosses (enforced at the proto).
//   - NON-GATING (absolute): everything here runs on the warm path, parallel
//     to detection. A clock failure is a DEGRADED state for the Tier-B panel
//     (doc 14 A13), never a perturbation of the deterministic tick. Nothing
//     in this package is digest-bearing.
//   - Bands never collapse to a line; "projected to cross", never "will".
//
// Determinism: given a fixed clock response, eligibility, the guards, and the
// projection are pure functions of (bindings, graph, readings, params) — the
// property the backtest harness (11 M5) and the M2 exit tests rely on.
package forecast
