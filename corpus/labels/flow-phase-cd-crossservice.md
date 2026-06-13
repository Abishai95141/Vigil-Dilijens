# Phase C/D — cross-service cascade in obsd — 2026-06-13

Phase D makes the captured flow edges *do* something: a WARM-PATH cross-service cascade
that, each eval tick, maps the tick's MEASURED findings to their workloads (the degraded
callees, via the identity store's role CEI), walks the observed-flow edges BACKWARD to
the impacted callers, and joins the ONE AUTHORED relation. Off the deterministic digest
(like the forecast lane), gated (logged, not yet a deterministic finding). Phase C (full
governance promotion into the core ontology graph) lands when this class passes its
backtest gate; until then the authored relation is surfaced verbatim from the
experimental overlay, charter-clean.

## What was built (branch `v2`)
- `obsd/internal/flow/crossservice.go` — `CrossServiceChain`: composes the chain from
  degraded workloads + the flow EdgeStore + the authored relation, reusing
  `StructuralCascade` (the digest-bearing core) so the surfaced chain matches a replay.
- `obsd/internal/flow/resolver.go` — role-CEI override: obsd stamps each pod with the
  AUTHORITATIVE identity-layer role CEI (`Kind="Deployment"`), fixing a latent
  cross-service MIS-JOIN (the spike used `Kind="Pod"`; findings map to `Kind="Deployment"`).
- `obsd/cmd/obsd/{flowcollect,main}.go` — the collector now resolves pods to identity
  role CEIs; the eval tick composes the cross-service cascade from live findings + flow
  edges + the relation and logs it (warm path, off the digest).
- `corpus/chaos/flow-cross-demo.yaml` — a controlled live scenario: a leaky CALLEE
  (fires while still serving) + a CALLER holding a connection (the flow edge).

## Verification
- **Unit (-race, network-free):** `CrossServiceChain` names the degraded hub as root,
  lists exactly its callers as impacted, EXCLUDES non-callers (discriminator), is
  charter-clean (no causal tokens), and returns no chain for empty/leaf degraded sets.
  The join is on the identity-layer role CEI (no mis-join).
- **Determinism (LIVE):** obsd `--flow-enabled` on the cluster — replay of the captured
  bundle (with the cross-service flow edge present, findings firing, and the warm-path
  cascade computing every tick) → **all 13 ticks byte-identical (doc 05 §3.5 holds)**.
  The cross-service cascade provably never perturbs the deterministic digest.
- **Flow edge (LIVE):** the collector captured **17 edges** = the 16 boutique edges +
  the new `flow-caller→leaky-callee` cross-service edge, with identity role CEIs.
- **Negative control (LIVE):** 4 real findings fired (on no-caller workloads, e.g.
  `leaky-worker`) and the cross-service cascade correctly produced **no false chain**.

## Honestly NOT yet demonstrated live (the one gap, with the cause + the fix)
The POSITIVE live path — a finding AND a flow-caller on the SAME workload — was not
captured: `leaky-callee`'s leak OOM-resets (~144s) before it sustains the rising slope
`PHEN_MEMORY_LEAK` requires, so the finding never coincided with the (captured) flow
edge. This is a detection-INPUT timing characteristic, not a cascade defect — the cascade
logic is unit-proven and every other half is live-verified. The clean live proof needs a
SLOW, non-OOMing sustained leak on the callee (the validated 09 M5 leak-creep pattern,
~6-8 min) OR the Phase C "degraded-but-alive" throttle phenomenon. Either fires
`PHEN_MEMORY_LEAK`/a degradation finding while the callee keeps serving → the cross-service
cascade names it root with the caller impacted. That run is the immediate next step.
