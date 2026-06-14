// Package events ingests discrete Kubernetes Events (OOMKilled, CrashLoopBackOff,
// …) as a DISTINCT, TYPED, MEASURED finding source and JOINS them — by exact CEI,
// never fused — to the gauge-fingerprint phenomena on the same workload role
// (v3 T-C). It closes a real blind spot: on a cluster whose cAdvisor reports
// container_oom_events_total == 0 (e.g. kind), the cgroup-OOM phenomenon never fires,
// yet the kubelet still RECORDS the OOMKilled fact — in the container's
// lastState.terminated.reason (the authoritative source, exitCode 137) and/or the
// Events stream. That discrete fact is the signal; the collector reads both sources.
//
// The load-bearing design constraint (doc 07): an event does NOT flow through the
// fingerprint-keyed matcher (detect.Match works over gauge/counter streams). An
// event is a discrete object, ingested as an events.EventFinding and resolved to a
// role CEI via the identity store — a finding-level join, never a fabricated
// fingerprint-stream member. We do NOT synthesize an OOMKilled-count gauge (that
// would double-count the kernel cgroup counter the gauge path already provides).
//
// Epistemic placement (doc 01):
//   - An EventFinding is MEASURED. "An OOMKilled event was emitted for this pod"
//     is a fact read from the API server, exactly like a threshold state.
//   - The "which event reason corroborates which phenomenon" mapping is AUTHORED —
//     a curated corroboration condition (events.Corroboration), surfaced verbatim
//     with author + version provenance, never invented by code.
//   - A corroboration is a topological CO-OCCURRENCE on a shared role CEI, not a
//     proof of cause. An event corroborates, it never causes; the words
//     cause/caused/root-cause appear nowhere in the output.
//
// These classes are JOINED adjacently in the surface (api.EventsView), each
// labelled, never fused into a single causal sentence.
//
// Determinism firewall (doc 05/11): this package NEVER participates in the
// deterministic tick. An EventFinding rides OFF the digest entirely — the digest
// hashes fingerprints/findings/cascades/unexplained (replay.Digest); events touch
// none of them. The collector runs in its own goroutine off the eval path; the
// join is recomputed each tick into an atomic snapshot the surface reads. obsd is
// byte-identical with --events-enabled off (the non-gating guarantee).
//
// Honest partial coverage: an event whose involved object the identity store has
// not seen resolves to its INSTANCE key with RoleUnresolved set — never a guessed
// role. A standalone event (a CrashLoopBackOff with no corroborating gauge
// phenomenon, or an OOMKilled with no cgroup-OOM finding on its role) is surfaced
// as a visible MEASURED finding, NOT auto-upgraded to a phenomenon match it lacks
// the required members for.
//
// Governance placement: the corroboration conditions live in an authored overlay.
// Until the events-gate passes they are an EXPERIMENTAL overlay loaded only by this
// lane (outside the production release hash, exactly as the cross-service relation
// began, doc 15 Phase A→C); promotion into the released graph is a governed
// follow-up. KSM object-state ingestion is a deferred sibling (separate gate
// dimension) — this prototype is EVENTS-ONLY.
package events
