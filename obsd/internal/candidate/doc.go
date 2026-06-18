// Package candidate is the firewalled staging store for the Dynamic Graph
// eXtension pipeline (doc 20): the PROPOSE → VERIFY → PROMOTE spine's "propose"
// sink. It holds PROPOSED/CANDIDATE data — proposed nodes, structural edges,
// phenomenon members, bar sources, and direction-free causal hypotheses — that an
// agent or a deterministic discovery pass stages for a NAMED HUMAN to promote
// through the governance gate.
//
// THE FIREWALL (doc 20 §1, the load-bearing invariant). This package and its
// SQLite database (candidates.db) sit OUTSIDE the graph loader and OFF the
// deterministic path. Two mechanical, test-enforced guarantees keep it that way:
//
//   - No detection/forecast package may import this package. The deterministic
//     packages (detect, selection, binding, observe, forecast, qss, graph,
//     clock, replay) must produce identical fingerprints/matches whether the
//     candidate machinery is present or absent. firewall_test.go asserts none of
//     them imports internal/candidate (transitively, via `go list -deps`).
//   - Candidate OVERLAYS live under ontology/graph/overlays/candidate/, a
//     subdirectory the loader's overlayPaths() skips — so they never enter a
//     release content hash. graph/overlay_firewall_test.go proves the released
//     hash is byte-identical with and without that subdirectory.
//
// PROVENANCE IS NOT EXTENDED. The three immutable classes stay
// MEASURED/PROJECTED/AUTHORED. A candidate carries an ORTHOGONAL lifecycle
// Status ∈ {candidate, promoted, rejected, shadow}: a stray metric's readings are
// MEASURED, but its proposed join/edge is merely status=candidate. The store
// authors nothing — promotion writes AUTHORED YAML into the root overlays via the
// governance gate, where a human authors the note, name, and version; the model's
// text is discarded at promotion.
//
// CHARTER GUARD (structural). A causal claim can never enter as an authoritative
// edge: KindEdge accepts only structural relations (topology / associated-with /
// topo-adjacent). A causal proposal must use KindCausalHypothesis, which is
// direction-free co-occurrence — surfaced labelled, never a cause.
//
// DETERMINISM. No time.Now in this package: every mutating call takes an injected
// `now`. The candidate set is explicitly NOT part of any replay guarantee (it is
// safe precisely because the deterministic path never reads it).
package candidate
