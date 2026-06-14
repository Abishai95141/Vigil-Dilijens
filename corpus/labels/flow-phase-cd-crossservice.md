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

## POSITIVE live proof — DONE (2026-06-14), two independent scenarios
Root cause of the earlier non-fire: my first leak workload was too fast for its small
limit (96Mi) — it OOM-sawtoothed every ~144s, so the working-set never held the SUSTAINED
rising slope `PHEN_MEMORY_LEAK` needs (the slope window is `rate_window: 5m`). Note the
twin trap: a PLATEAU above the bar also does not fire (rising-slope only — the #82 blind
spot; `leaky-worker` at 313/320Mi is the live example). The robust fix is a leak that
rises STEADILY through the at-threshold band `[bar, limit]` for longer than the 5-min
window without OOMing (`corpus/chaos/flow-cross-demo.yaml`: 384Mi limit, prefill 340Mi,
+1Mi/10s).

With that, the cross-service cascade FIRED LIVE, correctly, every tick:
- **Boutique fan-in** (productcatalog accidentally left squeezed to 18Mi → OOM-cycling,
  genuinely degraded): root = `online-boutique/productcatalogservice`, **3 impacted callers**
  (frontend, checkout, recommendation), `why_class=AUTHORED`, `edge_class=MEASURED observed
  flow`. Replay **byte-identical (33 ticks)**.
- **Chaos pair** (productcatalog healthy; leaky-callee the sole degraded callee): root =
  `chaos/leaky-callee`, **1 impacted caller** (flow-caller). Replay **byte-identical (8 ticks)**.
- The identity role-CEI join (Kind="Deployment") works for boutique AND chaos pods — no
  mis-join. The root selection is correct (higher-fan-in hub wins when several are degraded).

**Verdict: Phase D cross-service cascade is functionally complete and LIVE-PROVEN** —
discovers the call graph, ingests it into obsd's deterministic core (replay byte-identical),
and names the degraded cross-service root + impacted callers on the warm path, charter-clean.
The remaining lane work is E (forecast propagation along flow edges) and F (surfacing); and a
broader note: the cross-service cascade's COVERAGE rides the underlying finding coverage, so
the #82 plateau-blindness and a durable-OOM lane would widen which degradations it can trace.
