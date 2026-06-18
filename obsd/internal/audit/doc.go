// Package audit ingests the Kubernetes API-server AUDIT log as a DISTINCT, TYPED,
// MEASURED change source and JOINS each change — by exact CEI, never fused — to the
// incidents the deterministic path is already reporting (doc 20 P4, the AUDIT lane).
// It answers the first question every operator asks after an incident — "what changed
// just before this?" — without ever claiming the change CAUSED the incident.
//
// Epistemic placement (doc 01):
//   - A ChangeEvent is MEASURED. "A create/update/patch/delete of object X completed at
//     time T by user U" is a fact read from the audit log, exactly like a threshold
//     state. The change object resolves to a durable role CEI via the identity store
//     (the SAME resolution the events lane uses) — a finding-level join, never a fused
//     fingerprint stream.
//   - "This change might be related to that incident" is a PROPOSED candidate, not a
//     fact: a direction-free candidate.KindCausalHypothesis (relation
//     "observed-adjacency"), staged into the firewalled P0 store for HUMAN verification.
//     The lane authors nothing; promotion is a governed, named-human act.
//
// The ONE load-bearing operation is the ARROW-OF-TIME PRUNE (audit.Antecedents): a
// change at or AFTER an incident's onset can never be its antecedent, so it is pruned.
// This is a deterministic filter over MEASURED timestamps, NOT an inference of cause —
// and it is keyed strictly on the audit record's SOURCE completion timestamp
// (stageTimestamp), NEVER receipt/delivery time (webhook delivery is async and would
// corrupt the ordering). Temporal adjacency is a CO-OCCURRENCE (the ice-cream/drownings
// discipline), surfaced as a hypothesis, never a cause. The words cause/caused/
// root-cause appear nowhere in the output.
//
// Determinism firewall (doc 05/11): this package NEVER participates in the deterministic
// tick. A ChangeEvent rides OFF the digest entirely — the digest hashes fingerprints/
// findings/cascades/unexplained (replay.Digest); audit changes touch none of them. obsd
// is byte-identical with --audit-enabled off (the non-gating guarantee), enforced by
// firewall_test.go. The pure core (ParseEvents → Resolve → Antecedents → Hypothesize)
// is deterministic: same lines + same resolver ⇒ byte-identical change events and
// candidates, INVARIANT to the order lines arrive in (ParseEvents sorts + dedups by
// auditID). The live collector is non-deterministic only in WHICH lines it has read so
// far (the audit log is a sampled, append-only external source), exactly like events.
//
// Decoupling: the core imports neither identity nor client-go. Role resolution is an
// injected audit.Resolver (main backs it with the identity store) and the incidents to
// join against are passed in (main maps the active findings/incidents). The lane is a
// candidate PRODUCER, so it imports internal/candidate (the off-digest staging store),
// exactly as the dgx agent does.
//
// Honest partial coverage: a change whose object the identity store has not seen
// resolves to its own coordinate key with RoleUnresolved set — never a guessed role,
// and never an exact-CEI join. Such a change can still co-locate by NAMESPACE (the
// weaker tier, surfaced explicitly as joinTier="namespace"). A change with no
// co-located incident is still surfaced as a MEASURED change, never auto-upgraded.
//
// Governance: the AUDIT log is OFF by default on a kind apiserver; the live collector
// reads a JSONL audit-log path supplied by --audit-log-path (config-dependent, stated).
// The pure core + audit-gate are fixture-backed and hermetic, so the lane's correctness
// and determinism are proven without a live audit source.
package audit
