// Package detect is the "now" half of the runtime: deterministic phenomenon matching
// across the graph's topology — entity-local, first-order, second-order — plus
// cascade recognition and blast-radius derivation. The ontology's phenomena ARE the
// detection units; there is no separate playbook.
//
// Owning doc: 07-topological-detection-engine.md.
//
// Match semantics (doc 07 §3.1): a phenomenon lights up when its member signals
// reach their threshold states in the authored temporal pattern (T0- precursors,
// T0 event, T0+ consequence), required members mandatory, supporting members
// strengthening.
//
// Traversal contract (doc 07 §3.2, from doc 03): walks follow only the phenomenon's
// declared edge types, and EVERY traversed edge's validity interval must overlap the
// co-occurrence window. A condition "across" an edge not valid during the window is
// not a co-occurrence; a suspect edge DEGRADES a match, never supports a full one.
// This single rule prevents fabricating a 2-hop correlation through a stale edge —
// the worst trust failure available to the product.
//
// Output is MEASURED with AUTHORED references attached, never FUSED. A match says
// "these states co-occurred in this authored pattern across these entities"; the
// only "why" shown is the graph's authored notes, attributed. Degraded matches are
// marked with what was unobservable. A single crossing is NOT an alert (doc 07 §3.5).
//
// Determinism: same readings + same graph version + same topology snapshot => same
// matches, always. Detection NEVER waits on forecasting (non-gating rule, absolute).
package detect
