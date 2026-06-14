# Cross-service cascade backtest GATE — doc 15 phase D / doc 11 §3.5 — 2026-06-14

The gate that makes the v2 cross-service cascade operator-visible. Per the gate rule
(doc 11 §3.5), no warning/insight class ships to an operator before its backtest gate
passes. This is that exit for the cross-service surface (doc 15 phase F). **Verdict:
PASSED**, live-captured on the kind cluster, replayed byte-identically, scored 1.000
on every accuracy floor with zero false cascades and zero charter violations.

## What the gate is

The cross-service cascade is a DETERMINISTIC, MEASURED+AUTHORED structural claim: each
eval tick it maps findings → degraded workloads, walks the captured observed-flow
topology BACKWARD to the impacted callers, and joins ONE authored relation. Because it
is a pure function of `(findings, flow topology, relation, window)` — all pinned in a
replay bundle — it re-computes byte-identically off a capture. So the gate is a
REPLAY-SUBSTRATE backtest, exactly like the forecast gate (doc 11 M5):

1. `replay -crossservice` re-runs the REAL warm-path cascade at every recorded tick
   and writes one `TickCrossService` JSONL record per tick (root, impacted callers,
   degraded set, charter check). The findings→role mapping mirrors the live warm path
   (`store.Get(EntityCEI).RoleCEI`) using the captured bindings' role keys
   (`binding.RoleKey == pod.RoleCEI.Key()`) + `identity.ParseKey` — no live identity
   store needed, fully faithful. (`obsd/internal/replay/engine.go: crossServiceTick`.)
2. `harness/src/harness/crossservice_gate.py` scores those records against per-bundle
   GROUND-TRUTH labels and emits the PASS/FAIL/INSUFFICIENT verdict.

The pass is OFF the deterministic digest: every replay below also verified all ticks
**byte-identical** (doc 05 §3.5), proving the cross-service pass never perturbs the
tick — detection stays non-gated and replay-exact whether the cascade computes or not.

## Scoring discipline (why the floors are EXACT, not soft)

Unlike the forecast gate (statistical bands → soft floors), the cross-service cascade
is a measured structural function. Naming the wrong root or a phantom caller is a
mis-join or a bug, not noise, and the charter forbids mislabeling a cross-service
impact. So the accuracy floors are exact:

| metric | floor | rationale |
|---|---|---|
| ROOT accuracy | 1.0 | every fired tick must name the labeled degraded root exactly |
| CALLER recall | 1.0 | every recoverable ground-truth caller must be named |
| CALLER precision | 1.0 | a non-caller named impacted is a mis-join (zero tolerance) |
| FALSE cascades | 0 | a chain firing where silence was expected is the trust-killer |
| CHARTER | 0 | a causal-claim token means the join was fused, not joined |

Sufficiency mirrors the forecast gate (INSUFFICIENT is never a pass): ≥5 fired ticks,
≥2 distinct firing topologies (one lucky fan-in cannot carry the gate), ≥5 quiet ticks
(the no-false-cascade claim needs a quiet cluster to have been observed).

## The corpus (frozen in `corpus/crossservice/`)

Three labeled bundles captured live with `obsd --flow-enabled`, replayed with
`replay -crossservice`. Bundles themselves are not committed (per `corpus/bundles/`
convention); the small replay-output events JSONL + labels are the frozen golden,
regenerable by re-capture from the chaos manifests.

- **chaos-pair** (positive, single-caller) — `corpus/chaos/flow-cross-demo.yaml`:
  `leaky-callee` (degraded, MEMORY_LEAK on the rising-slope-at-threshold) ←
  `flow-caller` over one observed-flow edge. 13 ticks, **10 fired**, all
  `root=chaos/leaky-callee · impacted=[chaos/flow-caller]`.
- **chaos-fanin** (positive, 1→3 fan-in) — `corpus/chaos/flow-fanin-demo.yaml`:
  `leaky-hub` (degraded) ← three distinct caller roles. 14 ticks, **10 fired**, all
  `root=chaos/leaky-hub · impacted=[hub-caller-a, hub-caller-b, hub-caller-c]`.
- **healthy-negative** — boutique 12/12 healthy + `chaos/leaky-worker` present (loud
  but routed to the unexplained channel → no finding → degraded-but-no-caller, the
  correct quiet). 16 ticks, **0 cascades** — the no-false-cascade evidence.

Every bundle replayed **byte-identical** (16/16, 13/13, 14/14 ticks).

## Verdict (`just xsvc-gate`)

```
  [chaos-pair]       positive · 10/13 ticks fired · root 10/10 correct · caller recall 1.00 precision 1.00
  [chaos-fanin]      positive · 10/14 ticks fired · root 10/10 correct · caller recall 1.00 precision 1.00
  [healthy-negative] negative · 16 ticks observed · 0 false cascades (want 0)
  aggregate: 20 fired ticks over 2 distinct firing topologies; 16 quiet ticks
  root accuracy 1.000 · caller recall 1.000 · caller precision 1.000 · false cascades 0 · charter 0
  GATE: PASSED — class may become operator-visible (doc 11 §3.5 / doc 15 phase F)
```

Note the discriminator working live: in the pair bundle several workloads were degraded
the same tick (5 findings: leaky-callee, flow-caller itself, node-exporter), yet the
cascade rooted at `leaky-callee` (the degraded callee with a caller) and named exactly
`flow-caller` — no phantom, no missed caller, the no-caller degradeds correctly ignored.

## Reproduce

- Offline (frozen corpus): `just xsvc-gate` (or the `test_live_captured_corpus_passes`
  regression in `just harness-test`).
- Re-capture: `obsd --flow-enabled --store-dir=<dir>` while applying
  `corpus/chaos/flow-cross-demo.yaml` (then `flow-fanin-demo.yaml`), seal, then
  `replay -bundle=<dir> -crossservice -crossservice-out=<events.jsonl>`, then the gate.

## Standing notes / carry-forwards

- The cascade's COVERAGE rides the underlying finding coverage: the #82 plateau-
  blindness and a durable-OOM lane would widen which degradations it can trace. The
  corpus uses the proven slow-leak (rising-slope-at-threshold) so MEMORY_LEAK fires
  reliably; a real boutique productcatalog fan-in was already proven in Phase D
  (`flow-phase-cd-crossservice.md`).
- The two positive topologies are chaos workloads (real pods, real conntrack edges,
  real obsd findings, real cascade) chosen for firing RELIABILITY — the gate must be
  deterministic. They are not a proxy for the goal; they exercise the full
  conntrack→flow-edge→cascade pipeline end-to-end.
- Gate criteria are v1 (exact floors); they stay exact as the corpus grows (a
  deterministic class has no statistical slack to relax into).
