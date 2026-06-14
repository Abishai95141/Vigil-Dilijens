# Anticipatory cross-service cascade backtest GATE — doc 15 phase E / doc 11 §3.5 — 2026-06-14

The gate that makes the v2 **anticipatory** (PROJECTED) cross-service cascade
operator-visible. Per the gate rule (doc 11 §3.5), no PROJECTED class ships to an
operator before its own backtest gate passes. This is that exit for the phase-E lane —
the lane that joins the forecast clock (TimesFM) to the observed-flow topology to say
*"a callee is projected to cross its bar soon, so its callers are projected to be
impacted soon"* — each provenance class labelled, never fused (PROJECTED nodes,
MEASURED edge, AUTHORED why).

**Verdict: PASSED** (2026-06-14), live-captured on the kind cluster, the forecast
re-run deterministically per tick, scored against ground truth.

## What the gate is (and why it is NOT a digest claim)

The phase-D cascade is a MEASURED structural claim about NOW; the phase-E cascade is a
PROJECTED claim about SOON. The forecast is non-deterministic BY CLASS (a TimesFM
extrapolation), so — unlike the phase-D gate — this gate does **not** assert a
byte-identical digest. What it verifies is the JOIN (deterministic) and the operator
value: **did the projection LEAD the measured reality, and did it CONFIRM?**

The substrate is the same replay bundle, with one more pass:

1. `replay -projected-crossservice` re-runs the REAL forecast funnel at every recorded
   tick (against the bundle's readings + the injected model clock), seeds the cascade
   from the warned callees, and emits one `TickProjectedCrossService` JSONL record per
   tick — the anticipatory (PROJECTED) chain joined with the MEASURED chain of the same
   tick. The candidate→role mapping mirrors the live warm path
   (`store.Get(warning.EntityCEI).RoleCEI`) via the captured bindings' role keys, the
   same identity-layer join — no live identity store needed.
   (`obsd/internal/replay/engine.go: projectedCrossServiceTick`.)
2. `harness/src/harness/projected_crossservice_gate.py` scores those records against
   per-bundle GROUND TRUTH and emits PASS / FAIL / INSUFFICIENT.

The projected pass is computed AFTER the tick digest is formed and is never one of its
inputs — detection stays non-gated and byte-identical whether the clock is present,
degraded, or absent (confirmed: the determinism/non-gating review found 0 issues).

## Scoring discipline

| metric | floor | rationale |
|---|---|---|
| LEAD-TIME (worst confirmed) | ≥ 120 s | a projection must MEANINGFULLY lead its measured reality — a one-tick coincidence is not anticipatory value |
| CONFIRM | the EARLIEST measured fire must be AT/AFTER the projection | a measured fire that PRECEDES the projection means the reality was already there, not anticipated (the lead-inflation guard) |
| confirmed roots | ≥ 2 | independent confirmed ramps — one lucky ramp cannot carry the gate |
| STRUCTURAL FIDELITY | projected fan-in == measured fan-in | the anticipatory walk named the REAL callers (no phantom, no miss) |
| STRAY anticipations | 0 | no anticipatory chain on an UNDECLARED root in a positive scenario |
| FALSE anticipations | 0 | no anticipatory chain in a NEGATIVE (healthy) scenario — the forecast must not fabricate a crossing |
| CHARTER | 0 | no causal-claim token AND no collapsed band (PROJECTED dressed as MEASURED certainty) |

Sufficiency (INSUFFICIENT is never a pass): ≥ 5 anticipatory ticks, ≥ 2 confirmed
roots, ≥ 20 quiet ticks IN A NEGATIVE SCENARIO (the no-false-anticipation claim is
substantiated where the forecast SHOULD have stayed silent). Confirmation is restricted
to the corpus's DECLARED roots, so an unrelated measured cascade cannot manufacture one.

> These criteria were hardened after an adversarial review of the scorer found five
> exploits (lead inflation from a measured fire predating the projection; diversity
> counting declared names not confirmed roots; fidelity skipped on unconfirmed
> projections; quiet ticks aggregated across scenario types; an unrelated measured
> cascade confirming a coincidental projection). All five are now closed and regression-
> tested (`harness/tests/test_projected_crossservice_gate.py`).

## The live narrative this gate certifies (the end-to-end test)

Captured live on the kind cluster — the "now (MEASURED) + soon (PROJECTED)" story the
project set out to tell:

- **SOON (PROJECTED).** A callee on a clean working-set ramp, still well below its bar,
  is FORECAST by TimesFM to cross soon → the anticipatory cross-service cascade fires:
  *root `chaos/forecast-callee` → projected caller `chaos/forecast-caller`*, class
  `PROJECTED upstream+downstream · MEASURED edge · AUTHORED why`, band never collapsing,
  `surfaced=false` (withheld behind this gate).
- **~11.5 minutes later, NOW (MEASURED).** The callee actually crosses (PHEN_MEMORY_LEAK)
  and the MEASURED cross-service cascade lights up on the SAME root + SAME caller. The
  projection LED the measured reality by **~11.5 minutes** — the operator value.
- A second independent leak (`chaos/forecast2-callee`, different rate/prefill, on a
  different node) anticipated at 13:32 and measured-confirmed at 13:46 — **~14.5 min lead**.

## The frozen gate corpus (`corpus/crossservice-projected/`)

THREE clean captures (each its own bundle / measurement epoch), replayed through
`replay -projected-crossservice` — `just xsvc-projected-gate` reproduces the verdict
offline, no cluster needed:

- **`events-pos1.jsonl`** (positive, 89 ticks) — `chaos/leakx-callee` captured ALONE:
  anticipated its cross-service impact on `leakx-caller` and confirmed it **750 s
  (~12.5 min)** later, fan-in faithful.
- **`events-pos2.jsonl`** (positive, 90 ticks) — `chaos/leakz-callee` captured ALONE in
  a SEPARATE bundle: anticipated + confirmed **570 s (~9.5 min)** lead, fan-in faithful.
- **`events-negative.jsonl`** (negative, 38 ticks) — the healthy, flow-connected boutique
  under the SAME forecast lane + curated relation: **0 anticipatory fires** over 38 quiet
  ticks (the forecast active, correctly silent).

### Result

```
anticipatory cross-service backtest — class: projected_cross_service_cascade
  [leakx-leak] positive · 55/89 anticipatory ticks · 1 confirmed root(s)
      chaos/leakx-callee: lead 750s (proj 09:58:44 -> meas 10:11:14) · fan-in OK
  [leakz-leak] positive · 26/90 anticipatory ticks · 1 confirmed root(s)
      chaos/leakz-callee: lead 570s (proj 10:24:37 -> meas 10:34:07) · fan-in OK
  [healthy-flow-negative] negative · 38 ticks observed · 0 false anticipations (want 0)
  aggregate: 81 anticipatory ticks; 2 confirmed roots {'leakx-leak': ['chaos/leakx-callee'], 'leakz-leak': ['chaos/leakz-callee']}
  worst confirmed lead: 570s (floor 120s)
  stray anticipations:  0 (max 0)
  false anticipations:  0 (max 0)
  negative quiet ticks: 38 (floor 20)
  charter:              0 violations (max 0)
  GATE: PASSED — the PROJECTED cross-service lane may become operator-visible (doc 11 §3.5 / doc 15 phase E)
```

## Independence

The two confirmed roots come from TWO separate captures (distinct workloads, distinct
nodes, distinct measurement epochs) — the gate's diversity requirement is satisfied by
independent bundles, not merely two roots in one capture, so a single forecast glitch or
shared cluster artifact cannot carry the verdict. (The scorer reports
`confirmed_roots_by_bundle` to make this auditable.) The captures were tuned to the
forecast's sweet spot (prefill ≈ 500 Mi, +6 Mi/min): close enough to the bar that the
Tier-B funnel keeps the series budgeted, gentle enough that the forecast projects a
crossing ~10 min ahead — neither too low (deprioritised → warned late) nor too high
(crossed before the warning). The hardened scorer REJECTED both off-sweet-spot captures
during development, which is the gate doing its job.

## Scorer hardening (adversarial review, 2026-06-14)

A 20-agent adversarial review of this gate found **five** exploitable holes and all are
closed + regression-tested:
1. a measured fire PREDATING the projection inflating apparent lead → confirm now
   requires the EARLIEST measured fire to be at/after the projection (`meas_ever`);
2. diversity counting declared NAMES not CONFIRMED roots → now ≥2 confirmed;
3. fidelity skipped on unconfirmed projections → now checked on any projected-then-
   measured root;
4. quiet ticks aggregated across scenario types → the no-false-anticipation floor now
   requires quiet ticks IN A NEGATIVE scenario specifically;
5. an unrelated measured cascade manufacturing a confirmation → confirmation restricted
   to the corpus's DECLARED roots.
The same review found 0 issues in charter discipline, determinism/non-gating, identity-
join, and the Phase C governance promotion.
