// Package notify is the off-digest alert lane: it emails CLASSED facts the
// surfacing layer already published, and it is firewalled from the deterministic
// path exactly like internal/candidate (the read-firewall test in this package
// proves no replay-deterministic package imports it).
//
// Owning design: docs/30-alerting-lane.md (the where/when matrix + the fatigue
// controls). Charter (docs/01): every alert clause carries exactly one provenance
// class — MEASURED, PROJECTED, or AUTHORED — surfaced verbatim and labelled, never
// fused into a generated causal sentence. This package is a DUMB transport: it does
// not re-derive a class, does not paraphrase, and emits no "why" of its own. The
// classed content (the labels, the authored notes) is built by the mapping in
// cmd/obsd from the real surface views and handed in as a []Alert; this package only
// applies the fatigue controls, coalesces, renders, and sends.
//
// Discipline (all enforced by tests here):
//   - NON-GATING: detection never waits on notify; a send failure is logged and the
//     finding is retried on the next tick — it never perturbs the digest or replay.
//   - DEFAULT OFF: with --alerts-enabled absent the lane is never constructed, so the
//     deterministic path and replay are byte-identical to a build without it.
//   - NO LEARNED THRESHOLD: every knob (cooldown, short-lead split, rate, quiet hours)
//     is an operator-DECLARED constant read from env, never fitted.
//   - INJECTED CLOCK: the store and dispatcher take `now` as a parameter; no time.Now
//     in package logic (the SMTP send is the one wall-clock edge, off-digest by nature).
//   - CHARTER REGISTER: a PROJECTED clause reads "projected to cross", never "will";
//     a system-generated headline never contains a causal token (the AUTHORED note is
//     quoted verbatim and is the only place a curator's wording appears).
package notify
