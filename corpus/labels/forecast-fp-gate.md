# Forecast decision-layer FP gate — the near-miss/recede decoy corpus

Evidence for `just forecast-fp-gate` (harness.forecast_fp_gate + the Go drift guard
`forecast.TestNearmissCorpusFrozenConsistent` / `TestNearmissOracleHoldsAgainstProject`).

## Why this gate exists

The real-model backtest (`just forecast-gate`, evidence `forecast-gate-09M3.md`) only
ever scores bundles that **end in a real crossing**. In `harness.forecast_gate.score_class`
the false-warning count is only reachable when `actual_idx is None and cand and full_obs`
— a branch that is unreachable for a crossing-terminating bundle (`false_rate` returns NaN
when `candidates_full_obs == 0` and the floor is skipped). So `FALSE_WARNING_MAX = 0.30`
(and the marketed "0 false warnings") was asserted against an **empty set**. Recall was
validated; **precision was not**.

The decision code that owns the false-positive boundary is `forecast.Project`: it emits a
candidate only if the forecast **POINT** trajectory crosses the bar (`crossIndex(fc.Point,…)`),
and the §3.6 band-too-wide guardrail silences an unactionable cone. A series that climbs
toward its bar then **plateaus or recedes** must hit `SilenceNoCrossing`; that recede
defence was never driven by an adversarial fixture.

## What this gate certifies (and what it does NOT)

It folds the **real** `Project` over scripted forecast trajectories and freezes the
candidate-or-silence verdict, graded against an **independent label oracle**:

- **FP-ON-NEARMISS == 0 (CARDINAL)** — every recede / plateau / seductive-wide-band /
  band-too-wide scenario produces **no** candidate.
- **SILENCE-REASON exact** — a silenced scenario carries the exact `Silence*` reason
  (`no-crossing-within-horizon` vs `band-too-wide`), so silencing for the wrong reason fails.
- **RECALL** — every genuine-crossing control still emits (recall is not collateral damage).
- **PROJECTED-CLASS / CHARTER / PARAMS-PINNED** — emits are PROJECTED + isProjection; no
  causal/future-certainty language; the decision-boundary params are frozen in each label.

**SCOPE — read before citing:** this certifies **our deterministic decision code** — *IF*
the clock forecasts a non-crossing, the decision layer stays silent — and **NOT** TimesFM's
raw skill at forecasting a plateau (whether the model's POINT trajectory actually recedes
on a real near-miss workload). That is a separate, model-bearing, on-demand concern (the
live `just forecast-gate`). A pass here must never be read as "forecast precision validated"
or "TimesFM 0 false warnings". The gate's PASSED banner says exactly this.

## Scenarios (corpus/forecast-nearmiss/, 10; REQUIRED = 6)

| scenario | kind | expect | reason |
|---|---|---|---|
| recede-above | recede | silence | climbs to ~485 (bar 486) then recedes → no-crossing |
| plateau-below | plateau | silence | flat at 480, band [477,483] all below bar → no-crossing |
| seductive-wide-upper | seductive | silence | point flat below bar but q90 upper pokes above — Project gates on the POINT → no-crossing |
| band-too-wide | too-wide | silence | point crosses but the near cone spans >0.5×horizon → band-too-wide |
| imminent-crossing-above | crossing | **emit** | control: real imminent crossing, tight band |
| open-tail-crossing | crossing | **emit** | control: imminent + open far tail (LatestBeyondHorizon) |
| recede-below | recede | silence | below-bar mirror: dips toward 100 then rises → no-crossing |
| crossing-below | crossing | **emit** | control: genuine below-bar crossing |
| nan-point | robustness | silence | all-NaN points → defined SilenceNoCrossing, no panic |
| dip-then-cross | nonmonotone | **emit** | dips then crosses; crossIndex returns the first crossing |

## Teeth (proven)

The oracle is INDEPENDENT of the code: a regression that gated the candidate on the upper
quantile instead of the point would flip `seductive-wide-upper` to emit and fail both the Go
oracle test and the Python `FP-ON-NEARMISS` floor. Removing the band-too-wide guard fails
`band-too-wide`. Mutation tests in `harness/tests/test_forecast_fp_gate.py` exercise each
floor (FP, wrong-silence, recall, params-drift, charter, missing-required-scenario).

## Result

`just forecast-fp-gate` → **GATE: PASSED** — fp-on-nearmiss 0, silence-reason OK, recall OK,
projected-class OK, charter 0, params pinned; 10 scenarios graded, 4 emit controls. Hermetic
(no cluster, no model); runs in the harness CI job + `go test -race ./...`.
