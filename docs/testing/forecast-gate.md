# Forecast calibration gate — reproducible (Track 3)

> Closes audit-v3 roadmap **#1** ("forecasting unbacked / the quality gate grades a
> self-authored answer key"). The forecast *skill* was always validated in the 09 M3
> campaign; the gap was that it was a **one-off manual run**, not a re-runnable gate, and
> the only thing CI exercised was the scorer's arithmetic over hand-authored arrays. This
> track makes the real-model gate **one command** and labels the scorer test honestly.

## The two distinct things (don't conflate them)

| | What it checks | Where | Runs in CI |
|---|---|---|---|
| **Scorer unit test** | the *grading arithmetic* — band coverage, per-event recall, time-to-cross error, verdict thresholds — over hand-constructed traces. **Invokes no forecaster.** | `harness/tests/test_forecast_gate.py` | yes (harness job) |
| **Real-model gate** | the *real forecast pipeline's output* graded against the realized future from the same recorded series. **The gate of record.** | `just forecast-gate <bundle>` (CLOCK=timesfm) | on-demand + nightly plumbing smoke |

The scorer test's docstring now states this scope explicitly, so a reviewer opening it
can't mistake it for model validation (the audit's sharpest concern).

## The gate of record — `just forecast-gate`

```
just forecast-gate <bundle> [stub|timesfm]
```

Orchestrates the real pipeline end-to-end: starts `clockd`, runs `replay -forecast`
(re-running the REAL funnel + Tier-B budget + projection "as of" every recorded tick
against the clock), exports the realized readings to Parquet, and grades with
`harness.forecast_gate`. PROJECTED rides **off** the deterministic digest, so byte-identity
holds in every pass. The gate has the **INSUFFICIENT-never-pass** property: too few scored
forecasts → INSUFFICIENT, never a hollow green.

- `timesfm` — the real model (gate of record). Needs the clockd `model` extra: a heavy
  torch + checkpoint download on first run.
- `stub` — the zero-knowledge clock (flat, no skill): for verifying the *plumbing*. Its
  correct verdict is INSUFFICIENT — never read it as forecast validation.

## What this session verified vs. what stands on prior evidence

- **Verified live this session — the plumbing.** `just forecast-gate <bundle-v1> stub`
  ran the full chain (clockd → `replay -forecast` → 8 ticks byte-identical → Parquet
  export → grade) and correctly returned `GATE: INSUFFICIENT — 0 scored < 5 (never a
  pass)`. The reproducible wiring works and the gate honestly refuses a hollow pass.
- **Stands on committed evidence — the model skill.** The real-model gate
  (CLOCK=timesfm over slow-creep captures) was validated in the **09 M3 campaign**:
  **3,870 scored forecasts / 213,978 realized band points → band coverage 0.755, event
  recall 1.00 (3/3 class-eligible creep events warned ~5 min ahead), 15/16 crossings
  in-band, 0/16 false warnings → GATE PASSED.** Trail: `corpus/labels/forecast-gate-09M3.md`;
  decomposition (09 M5) follow-up: `corpus/labels/decomposition-09M5.md`.
- **Not re-run this session — the full real-model campaign.** Re-running CLOCK=timesfm
  to a PASS needs the multi-GB model **and** a fresh ~20-min slow-creep capture (the
  bundle-v1 fixture is a short detection fixture with no forecast-eligible crossing). The
  gate is now reproducible on demand; the GB-model + long-capture run is the documented
  next step (and the nightly CI lane below proves the plumbing stays intact meanwhile).

## CI

- The **scorer unit test** runs on every PR (the `harness` job) — a real pass/fail on the
  grading math.
- A nightly **`forecast` plumbing smoke** (`.github/workflows/integration.yml`) runs the
  stub gate and asserts a verdict is produced — catching pipeline rot (a broken
  `replay -forecast` or a gate API drift) without a model download.
- The real-model gate is **on-demand** (too heavy for routine CI). Wiring it into CI is
  gated on caching the model + checkpoint — a documented follow-on, not a silent gap.

## Capturing a real bundle for the gate of record

The gate needs a bundle whose gauge **creeps across a LEVEL bar over the context window**
(>~16 min at the 15 s scrape) — `corpus/chaos/leak-slow.yaml` / `leak-saw-slow.yaml` are
built for this. Capture live with `obsd --store-dir <dir>` against the chaos workload, seal
on shutdown, then `just forecast-gate <dir> timesfm`. (The fast `corpus/chaos/e2e/`
leaks cross within ~1 min — good for *detection* e2e, too fast for *forecast* context.)
