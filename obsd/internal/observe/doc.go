// Package observe is the deterministic measurement substrate: the three primitives
// that are the system's ENTIRE statistical vocabulary, and the per-entity
// fingerprints that detection reads. Everything here is arithmetic; nothing learns.
//
// Owning doc: 05-observation-and-fingerprint-pipeline.md.
//
// The three primitives (the closed, complete set — doc 05 §3.2):
//   - Threshold:      is the latest value past its resolved bar (subtraction)
//   - Rate-of-change: smoothed first difference over a window (differencing)
//   - Co-occurrence:  several conditions true at once within a window (conjunction)
//
// Fingerprints (doc 05 §3.4) materialize per selected entity: a threshold-status
// ladder (below | at-threshold | above | well-above) per thresholded variable, rate
// summaries, and a co-occurrence window state — each stamped with evaluation time
// and the bar source it used (config | override | default-flagged).
//
// Determinism (doc 05 §3.5): same readings + same bars + same windows => same
// fingerprints, always. This path is replayable by construction (doc 11).
//
// Do NOT: add any operation beyond the three primitives — no baseline, no seasonal
// model, no distribution fit, no anomaly score exists on this path, by commitment.
package observe
