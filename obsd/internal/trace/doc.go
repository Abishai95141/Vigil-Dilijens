// Package trace turns OpenTelemetry spans into the OBSERVED service call graph — a
// DISTINCT, MEASURED structure source — and PROPOSES each discovered service-to-service
// call as a structural topology candidate for human promotion (doc 20 P4, the TRACE
// lane). It closes the "no L7 call-graph" blind spot the gap analysis named: the
// deterministic path sees TCP flow (conntrack) but not which service called which, nor
// per-call latency/errors.
//
// Epistemic placement (doc 01):
//   - A CallEdge is MEASURED. "Service A invoked service B N times, p95 X ms, E errors,
//     over this span batch" is a fact aggregated from the spans, the same class as a
//     fingerprint. The parent→child ARROW is OBSERVED STRUCTURE (who called whom), NOT an
//     inferred cause: a slow callee does not "cause" a slow caller here, and the words
//     cause/caused/root-cause appear nowhere in the output.
//   - "This observed call should be an authored topology edge" is a PROPOSED candidate, not
//     a fact: a candidate.KindEdge with relation "topology" (a STRUCTURAL relation, never
//     causal — the candidate store rejects a causal edge by construction), staged into the
//     firewalled P0 store for a NAMED human to promote. The lane authors nothing. This is
//     the SAME propose→verify→promote path the flow lane's cross-service edges graduated
//     through (doc 15 phase C).
//
// Borrowed normativity (doc 01): the lane MEASURES latency (p50/p95/max) but NEVER derives
// a cutoff from the distribution — a data-fit "slow" threshold is a learned quantity,
// charter-forbidden. A latency is "slow" only against a DECLARED SLO (vigil.io/slo.*),
// resolved separately; where none is declared, the latency is surfaced unbounded, never
// judged.
//
// Determinism firewall (doc 05/11): this package NEVER participates in the deterministic
// tick. Spans are SAMPLED and census-incomplete — they ride OFF the digest entirely
// (obsd is byte-identical with --traces-enabled off, enforced by firewall_test.go). The
// pure core (ParseSpans → BuildCallGraph → ProposeTopology) IS deterministic: same spans
// ⇒ byte-identical call graph + candidates, INVARIANT to span arrival order
// (BuildCallGraph sorts + aggregates in fixed order; percentiles are computed over sorted
// durations). The live collector is non-deterministic only in WHICH spans it has sampled
// so far — and that incompleteness is COUNTED and surfaced (OrphanSpans, the honest
// partial-coverage discipline), never hidden.
//
// Decoupling: the core imports neither client-go nor an OTLP SDK. The collector maps real
// OTel spans onto the minimal trace.Span (exactly as the events lane mirrors
// ObjectReference). Service→CEI resolution is an injected ServiceResolver (main backs it
// with the identity store) so the core stays decoupled. The lane is a candidate PRODUCER,
// so it imports internal/candidate, exactly as the audit lane + the dgx agent do.
//
// Governance: neither demo cluster runs an OTel/Jaeger/Tempo span source, so the live
// collector reads a spans JSONL path (--traces-path) an operator wires from an OTel file
// exporter — config-dependent, stated. The pure core + trace-gate are fixture-backed and
// hermetic, so the lane's correctness and determinism are proven without a live source.
package trace
