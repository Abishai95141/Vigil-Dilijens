// Package flow is an EXPERIMENTAL hypothesis-validation spike (not on the
// production path). It tests one claim: that service→service dependency edges can
// be DISCOVERED automatically by observing Linux conntrack — a MEASURED network
// fact — and that the existing cascade machinery, given those edges plus ONE
// authored cross-service relation, can connect interdependent failures into a
// named root chain.
//
// Epistemic placement (doc 01):
//   - A flow edge is MEASURED. It is surfaced as "observed flow", never as
//     "depends-on"/"calls"/"requires". Observing that A repeatedly opened TCP
//     connections to B is a fact, not an inferred dependency.
//   - The cross-service "why" is AUTHORED — one curated phenomenon_relation
//     (callee-degradation → caller-impact), surfaced verbatim with provenance.
//   - The "root" is a STRUCTURAL position over the observed graph (the most-
//     upstream degraded node), MEASURED + topological. It is NOT a causal claim;
//     the words cause/caused/root-cause appear nowhere in the output.
//
// These three are JOINED adjacently in the output, each labelled, never fused
// into a single causal sentence.
//
// Determinism firewall (doc 05/11): this package NEVER participates in the
// deterministic tick. It is a separate binary (cmd/flowprobe) that builds its OWN
// identity.EdgeStore, never calls RunCycle/Digest, and emits to a warm side
// channel. The EdgeType("flow") is package-local: it is deliberately absent from
// graph.knownTraversalEdgeTypes and params.requiredEdgeBudgets so it can never
// enter an EdgeSnap, a TickResult, or the replay digest.
//
// Honest partial coverage: cross-node calls SNAT-masked to the node bridge are
// caller-unrecoverable and are COUNTED (snat_masked_flows), never fabricated.
// The recoverable subgraph relies on durable ESTABLISHED rows; low presence is
// surfaced as degraded confidence, not a confident edge.
//
// Scope it does NOT cover (deferred to a real lane): a production collector
// (DaemonSet/eBPF), pre-SNAT cross-node caller recovery, wiring flow into the
// production detect.related()/Cascades walk while keeping replay byte-identical.
package flow
