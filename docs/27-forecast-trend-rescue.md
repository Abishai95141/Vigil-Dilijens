# 27 — Forecast eligibility: the flat-series blind spot + the CUSUM trend rescue

> Status: BUILT + unit-validated; live A/B on `kind-vigil-abb`. Owning package:
> `obsd/internal/forecast` (`trend.go`, `runner.go`); params `obsd/internal/params`;
> surface `obsd/internal/api/warnings.go`. Charter: doc 01. Companion: doc 09 (forecasting),
> doc 22 C2 (the onset detector this reuses).

## The question

"How do we decide that we will forecast something, and does it hold up in production?"

The forecast funnel (`obsd/internal/forecast/eligibility.go` → `selection/tierb.go`) makes a
series **eligible** iff it has a *declared bar* and budgets the eligible set (50/cycle,
precursor-first). Then, per cycle, the runner applies a **dynamics guard**
(`runner.go`): a series with too little movement is silenced as `flat-series` so the scarce
clock budget is spent on series that are actually doing something.

That guard was the weak link.

## The blind spot (confirmed numerically)

`flat()` measured the **coefficient of variation** of the 64-point tail: `stddev/|level| <
flat_epsilon` (default 0.005) ⇒ silence. CV is an **amplitude** statistic, and a leak's
onset is about **direction/persistence** — so a series flat for 60 points with a small creep
in the last 4 is dominated by the quiet majority and scores "flat":

| Onset stage | Last value | CV | flat() verdict (eps=0.005) |
|---|---|---|---|
| very early (+0.9%) | 100.9 | 0.0012 | **silenced (leak missed)** |
| early (+2%) | 102.0 | 0.0028 | **silenced (leak missed)** |
| moderate (+5%) | 105.0 | 0.0069 | forecast |

Worse — and this is why it can't be tuned away — the populations **overlap**: a
noisy-but-trendless series (CV 0.0123) scores **higher** than a real early creep (CV 0.0028).
Lowering epsilon to catch the creep forecasts all the noise; raising it misses more creeps.
Amplitude cannot separate onset from noise. The right signal is a **changepoint detector**.

## The fix — a CUSUM rescue (`forecast/trend.go`)

The gate becomes: **silence as flat only if `flat()` AND there is no recent sustained
onset.**

`Trend(series, p)` is an **EWMA-residual CUSUM with sustained-shift confirmation** — the same
proven detector as the off-digest onset producer (doc 22 C2: 0/400 false-onset on stationary
noise, 400/400 detect on a real 6σ step), reimplemented on the bare float series. It reports
whether a sustained directional drift is **in progress at the tail** (begun within
`trend_recent_window` points of the end — an old step that has since plateaued is the
regime-shift detector's jurisdiction, not this one), the drift direction, and where it began.

Because the baseline's robust sigma is tiny on a stable memory series, even a *small*
sustained creep is many residual-sigmas and trips the CUSUM — while pure noise, lacking
persistence, does not (the sustained-shift floor `trend_min_z` drops a transient spike).

### Cold-start confidence (the "few points of new slope" problem)

When a series is rescued but its new slope is still short (`< trend_min_onset_points`), the
projection is **early and low-confidence**. We do **not** fabricate or widen the band
(charter); we **flag** it — `EarlyOnset` on the candidate, surfaced as a caveat on the
warning card (`earlyOnset` field), exactly like the regime-shift contamination flag. As the
slope establishes, the flag clears and the band firms. The operator sees an honest "this just
started, confidence is low, it will sharpen" rather than either silence or false precision.

## Charter compliance

- **Off the digest, non-gating.** Lives entirely in the forecast warm path; detection and the
  replay tick digest are byte-identical with it on or off (the replay determinism suite still
  passes). It changes only *which series are eligible to forecast* — it produces no statement
  and feeds no detection, digest, or governance.
- **Borrowed-normativity / no learning.** Every constant (`trend_cusum_k/h`, `trend_min_z`,
  `trend_warmup`, `trend_recent_window`, `trend_min_onset_points`) is a *declared* param,
  versioned in `defaults.dev.yaml`, never learned online.
- **Safe rollback.** `trend_rescue: false` (or `trend_cusum_h <= 0`) makes the gate
  byte-identical to the old `flat()`-only behaviour (asserted by a unit test).

## Validation

- **Unit battery** (`trend_test.go`, `go test -race`): the three early creeps above are
  rescued (gate no longer silences); a 400-series stationary-noise battery false-trips at
  **1.0%** (< 2% bar — the rescue does not flood the budget); truly-flat stays silenced; an
  old-plateau step is correctly NOT treated as in-progress; the new-slope length is correct;
  disabled ⇒ byte-identical; deterministic.
- **Live A/B** on `kind-vigil-abb`: a *gentle* leak (48 KiB/cycle — added as a `leak_kb` knob
  on the sim) creeps pdm-analyzer's working set. Two obsd instances watch the same series:
  `:9097` (`trend_rescue: false`, the old gate) silences it as `flat-series`; `:9096`
  (`trend_rescue: true`) keeps forecasting it.

  **Live result (2026-06-21):** with the leak off, both instances correctly silence
  pdm-analyzer's `container_memory_working_set_bytes` as `flat-series`. At 11:29:11Z a gentle
  20 KiB/cycle creep starts; ~1.7 min later, at mem **38.92 Mi — just +1% above the 38.5 Mi
  baseline** (deep in the low-CV "flat" zone, CV ≈ 0.003):

  | instance | gate | verdict for the creeping series |
  |---|---|---|
  | `:9097` | `trend_rescue: false` (old) | **`silenced: flat-series`** — the leak onset is MISSED |
  | `:9096` | `trend_rescue: true` (new) | **`no-crossing-within-horizon`** — RESCUED; now forecast |

  The new gate caught a +1% creep the old gate dismissed as flat — on a real cluster, exactly
  the late-onset case. (`no-crossing-within-horizon` is the *correct* live reason: the series
  is being forecast but is still far from its 243 Mi bar, so no card fires yet — it would warn
  as the creep approaches, which the old gate never would because it stayed silenced.)

## A note on lead time (the related worry)

Lead time ≈ `(bar − value) / ramp_rate`, bounded by when the model confidently projects a
crossing within the horizon. The short demo leads were the **fast demo leak** (4 MiB/cycle),
not context buildup (the cards carried ~57 context points) and not detection (a separate,
non-gating lane). The new `leak_kb` knob lets a gentle, realistic ramp demonstrate the longer
lead the same forecaster gives — and this trend rescue is what keeps that gentle ramp from
being silenced as "flat" in the first place.
