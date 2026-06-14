// Package mcp is a READ-ONLY Model-Context-Protocol adapter over Vigil's
// already-classed surfacing payloads (doc 10 / the T-A track of the v3 plan).
//
// Owning intent: expose to an LLM client the truths Vigil computes deterministically
// — coverage, the silence ledger (deterministic ABSENCE), and the PROJECTED early
// warnings with their mandatory bands — each carrying its born provenance class,
// verbatim. The marquee tool is the silence ledger: a provable NEGATIVE ("what are
// we NOT watching, and why") is exactly what an LLM hallucinates and Vigil can state.
//
// CHARTER (do / don't):
//   - READ-ONLY, no write-back. This package imports ONLY internal/api (view types +
//     the CharterViolations linter) and the standard library. It holds no graph,
//     findings, binding, or store WRITER. The one generative affordance —
//     emit_advisory — returns a labelled ADVISORY 4th class to the caller and writes
//     nothing anywhere. No learned edge/weight/threshold can originate here.
//   - NON-GATING. The server reads immutable per-tick snapshots via the Sources
//     funcs; the deterministic eval/detect path never calls into it. With the
//     server absent or disabled, detection is byte-identical.
//   - CLASS INTEGRITY. Tool results are the api.*View JSON verbatim — MEASURED stays
//     MEASURED, a PROJECTED warning keeps its isProjection mark and its
//     non-collapsing band. ADVISORY is a distinct class, never surfaced as MEASURED.
//   - ADVISORY is gated. Even a register-clean advisory is WITHHELD until its
//     backtest gate passes (doc 11 §3.5); a draft with a banned causal/certainty
//     register is REFUSED outright.
package mcp
