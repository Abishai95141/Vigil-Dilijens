# Vigil — every piece of math, and why it's there

The guiding idea first, because it explains every choice below:

> **Vigil keeps the deterministic detection path made of the simplest possible math — subtraction,
> differencing, and AND — so the same inputs always give byte-identical outputs. The only "real"
> statistics (CUSUM, Pearson, a forecast model) are kept *off* that deterministic digest, and every
> constant is *declared*, never fit to the data it judges.** A cutoff learned from the data would be a
> threshold Vigil invented — which the charter forbids.

So the math splits into two worlds: the **closed deterministic core** (replay-identical), and the
**off-digest statistical lanes** (annotate, never decide). Here's all of it.

---

## Lane 1 — Detection core: a closed set of 3 primitives

Every phenomenon is matched by composing exactly three arithmetic primitives. No model, no fit.

**1. Threshold ladder (subtraction).** A value vs. a config-supplied bar, as a 4-rung state machine:
`below → at-threshold → above → well-above`. For an "above" bar: `at-threshold` when
`value ≥ bar·(1−band)`, `above` when `value ≥ bar`, `well-above` when `value ≥ bar/factor` (the raw
limit). *Why:* the bar comes from the operator/borrowed-normativity, so a crossing is pure subtraction —
deterministic, and the `at-threshold` rung is a free "approaching" early signal.

**2. Windowed rate (differencing).** Per-second change of a counter over a window, made honest:
samples sorted by time; any gap `> 2×scrape-interval` truncates to the contiguous run; any *negative*
delta is a counter reset — counted, never summed or extrapolated. `rate = Σ(positive Δ) / elapsed`.
*Why:* measures "how fast is this climbing" (restart storms, throttling) without ever inventing a spike
from a counter wrap or a scrape gap. The gauge version (slope) does the same without reset-segmentation
(a gauge may legitimately fall).

**3. Co-occurrence (conjunction).** ANDs the member booleans: `all-met / partial / none` (empty set →
`unknown`, never spuriously all-met). *Why:* a phenomenon is usually "A and B and C at once"; this is
the join.

**Supporting arithmetic on top of the 3:**
- **Completeness ratio** = `required-members-met / required-total`. Quality is `full` only if nothing
  required was unobserved, else `degraded`; a degraded match below a pinned floor doesn't surface.
  *Why:* turns a multi-part match into a graded "how much evidence do we actually have," and suppresses
  thinly-supported alerts.
- **Cascade pairing** — a downstream finding pairs with an upstream trigger only if (a) it's a *declared*
  trigger, (b) the two are topologically related, and (c) the trigger preceded it (arrow of time), inside
  a bounded window. *Why:* recognizes a multi-step cascade as one story using an authored relation + time
  order, never a learned correlation.
- **Histogram → quantile (p95/p99).** Standard Prometheus bucket interpolation:
  `rank = φ·total`, then linearly interpolate inside the rank bucket:
  `lower + (upper−lower)·(rank−c_lower)/(c_upper−c_lower)`. Quantile set `{0.5, 0.95, 0.99}` is a
  *declared, versioned constant — never fit to the data*. *Why:* unlocks p95/p99 latency from any
  histogram exporter as ordinary gauge series the threshold ladder can then check — with no new primitive.

---

## Lane 2 — Anomaly / "when did it start" (the onset lane, off-digest)

This is the first place real statistics appear. It only **timestamps** a change that's already happening;
it never decides severity, and it's kept out of the deterministic digest.

**4. EWMA baseline.** An exponentially-weighted moving average `b[i] = α·x[i] + (1−α)·b[i−1]`, `α=0.10`.
The *residual* is one-step-ahead: `resid[i] = x[i] − b[i−1]` (how surprised are we by this point given the
recent baseline). *Why:* a cheap, online "expected vs. actual" with no model to train.

**5. Robust sigma via MAD.** Noise scale `σ = 1.4826 × median(|x − median(x)|)`, floored at 1e-9.
The 1.4826 makes the median-absolute-deviation a Gaussian-consistent σ. *Why:* a normal standard deviation
is wrecked by the very spike we're hunting; the median-based version isn't. We standardize: `z = resid/σ`.

**6. Two-sided CUSUM changepoint.** Two cumulative sums of the standardized residual:
`g⁺ = max(0, g⁺ + z − K)` and `g⁻ = max(0, g⁻ − z − K)`; alarm when either exceeds `H`.
Constants `K=0.5` (per-step slack), `H=4.0` (decision threshold), both in σ-units, tuned by an in-repo grid
sweep for a low false-alarm rate on stationary noise. *Why:* CUSUM accumulates *small but persistent*
excursions, so it pinpoints the **moment a level shift began** — including a sub-threshold creep that never
trips any bar. A simple threshold can't find a "started drifting but hasn't crossed yet."

**7. Sustained-shift confirmation.** A CUSUM alarm becomes a real onset only if the *median* of a window
after it differs from the median before it by `stepZ = |post−pre|/σ ≥ MinZ (=3.0)`, and the direction
agrees. *Why:* a transient spike settles back so its before→after median shift is ≈0 and is dropped; a real
step persists. Medians (not means) keep this robust to outliers. Then **backtracking** walks the alarm
index backward to report where the step *began*, not where the sum finally tripped.

---

## Lane 3 — Forecasting / early-warning (off-digest, "warm path")

Two charter rules thread through all of it: **every projection carries a mandatory uncertainty band
(never a bare line)**, and **silence is the default — every non-firing target reports a reason.**

**8. TimesFM 2.5 quantile forecast.** A pretrained zero-shot time-series foundation model (Google
TimesFM-2.5-200m). Input = the last up-to-1024 raw floats; output = a point trajectory + decile quantiles
(q10…q90) over a 240-step horizon (240 × 15s = **1 hour**). *Why:* gives multi-step forecasts *with native
uncertainty quantiles* and **no per-customer training** (no learned per-customer values — charter). The
wire is bare floats only — no cause/bar/identity ever crosses into the model.

**9. Uncertainty band (quantile fan).** The band is the p10 and p90 trajectories around the point estimate;
it *widens with horizon* (in the fallback, ∝ √(h+1)) and is floored so it can never collapse to a line.
*Why:* honest, growing uncertainty at longer leads — the charter forbids false precision.

**10. Affine band calibration.** `q' = point + (q − point)·cal`, scaling the band half-width by a single
declared constant `cal` (default 1.0). `cal≤0` is forbidden (would collapse the band). *Why:* aligns the
nominal 80% band to the *empirically observed* coverage from the offline backtest — a declared,
offline-derived, customer-invariant constant, not online learning. It leaves the point untouched and keeps
quantile order, so "bands never collapse" holds by construction.

**11. Crossing-within-horizon test (the core).** Find the first step where the point trajectory crosses
the bar; if it never does within the horizon → silence `no-crossing-within-horizon`. The band edges give
the honest `[earliest, latest]` window (the optimistic quantile crosses soonest, the pessimistic latest).
*Why:* a warning is only warranted if the forecast *actually* reaches the authored bar in time; the
"already past the bar now" case is handed back to detection, not forecast.

**12. Lead time.** `time-to-cross = (crossing-step + 1) × 15s`, anchored to the last *observed* sample's
timestamp (not wall-clock). *Why:* reproducible — same forecast + same bar ⇒ same verdict. (Note it's the
*trajectory* meeting the bar, not a `headroom ÷ slope` shortcut.)

**13. Band-too-wide guard.** Silence if the near cone (earliest→point) spans more than half the horizon —
judged *absolutely* as a fraction of the horizon, **never relative to time-to-cross** (dividing by ttc would
suppress a tight imminent warning exactly when it matters). Confidence class: `tight / moderate / wide`.

**14. Footprint decomposition.** Before forecasting, splice the history at the most recent reset or
operator-declared window and forecast only the clean remainder. *Why:* fed a ramp→OOM→reset *sawtooth*, a
zero-shot model would predict the next *reset*, not the bar crossing. We remove the footprint from the model
*input* while the *reason* stays in the graph (charter: pull the footprint, not the reason). It aborts
rather than over-trim if it would explain away >90% of the series.

**15. Gauge-reset detector (3 conditions).** An index is a confirmed restart only if: a >40% relative drop,
**and** the drop is large vs. the series span (so proportional noise on a near-zero baseline doesn't fire),
**and** it *persists* (doesn't bounce back to ~90% within 4 points). *Why:* distinguishes a real container
restart (cgroup gauge resets to 0) from a GC free / cache eviction / single noisy scrape.

**16. Regime-shift / "step-that-plateaus" detector.** Finds an *upward* step between two roughly-flat
regimes (boundary maximizing the jump in segment means; new level must be ≥35% higher on robust medians;
both sides must be flat). Only *flags* — never trims. *Why:* an undeclared deploy that *raises* the baseline
toward the bar leaves no reset, contaminating the forecast. The **cardinal rule**: an ongoing upward *ramp*
is the leak we exist to catch, so this fires only on a step that **plateaus**, and cleaning needs an
operator-declared window.

**17. Trend-rescue (CUSUM again, in forecasting).** Same EWMA-residual CUSUM as the onset lane, used to
*rescue* a series that's flat for a long time then begins a late creep — which the variance test would
wrongly silence. *Why:* the earliest, most valuable moment to warn about a leak is right when it starts;
direction/persistence, not amplitude, is the right signal. Adds an `early-onset` low-confidence flag when
the drift is only a few points old (labels trustworthiness; never fakes a tighter band).

**18. flat() low-variance silence.** Silence a series if `stddev/level < 0.5%` over the recent 64 points.
*Why:* a series with no dynamics has nothing to project; forecasting it would be noise-chasing.

**19. Churn-stable role-series.** Forecast the durable *workload role* (from OwnerReference), not a mortal
pod UID. Bin all member streams; in each bin take the **worst member toward the bar** (max for an above-bar,
min for a below-bar); omit empty bins (never zero-fill). *Why:* a per-pod forecast dies the instant HPA /
rollout / OOM replaces the pod (new UID = lost history). A *sum* would scale with pod count and falsely
"cross" a per-pod bar with two healthy pods — so the right question is "is the **worst** member about to
cross its **own** bar." Identity succession is authored; the aggregation is measured — joined, never fused.

---

## Lane 4 — Association & causal hypotheses (off-digest)

This lane finds *links* — and deliberately stops before *direction*. A human authors the arrow.

**20. Windowed Pearson correlation.** `r = Σ(xᵢ−x̄)(yᵢ−ȳ) / √(Σ(x−x̄)²·Σ(y−ȳ)²)`, over 15s bins both
series observe, kept only if `|r| ≥ 0.6` over ≥8 overlapping bins. *Why:* a MEASURED, **undirected**
("associated-with") coupling — symmetric, so it can never be read as a causal arrow. The cluster loop is
O(n²)-bounded by a 256-stream cap.

**21. Partial-correlation prune (the first step of the PC algorithm).** For an edge a~b and a neighbor c,
`pc = (r_ab − r_ac·r_bc) / √((1−r_ac²)(1−r_bc²))`; drop the edge if `|pc| < 0.5` (c "explains away" the
co-movement — a common driver or transitive path). *Why:* cuts confounded/transitive co-occurrences so the
operator reviews a handful, not thousands. Only ever *removes* edges, never directs one.

**22. Lead-lag witness (detrended cross-correlation + permutation p-value).** First-difference both series
(so a shared linear ramp differences away), then find the lag `k` maximizing `|Pearson(Δa[i], Δb[i+k])|`
over a ±60s grid. Significance via a **seeded circular-shift permutation null**:
`p = (1 + #{|r_perm| ≥ |r_obs|}) / (1 + K)`, K=200 shuffles, significant if `p < 0.05`; a lag-0 (simultaneous)
peak → honest silence, not "0s lead." The seed is derived from a hash of the pair+window, so it's
replay-identical. *Why:* gives a *sign-carrying clue* ("A appeared to lead B") for a human — never
auto-converted to an arrow (auto-direction from co-movement invents false edges).

**23. Co-onset staging + offline PCMCI+.** A direction-free candidate is staged only if both ends of an
associated pair register an onset within a window of each other (cross-workload only), ranked by
`|coefficient|` then tightest co-onset gap. Offline, a PCMCI+ pass (ParCorr conditional-independence on a
fixed lag grid) produces a *ranked candidate shortlist* whose direction is a hint a human authors. *Why:*
direction is the one thing Vigil refuses to infer; it stages the evidence and hands the pen to the operator.

---

## Lane 5 — Right-sizing (advisory, deterministic)

**24. Sustained percentile.** Nearest-rank `p95` over the window: sort, take index `ceil(0.95·n)−1`.
*Why:* the workload's real demand envelope to size against — not a single-shot reading.

**25. Coefficient of variation (stability gate).** `CV = stddev / |mean|`; advise only if `CV ≤ 0.5` and
no active onset. *Why:* a churny / ramping / mid-rollout workload gets honest silence ("unstable"), not a
misleading number.

**26. The rules.** Resize-up if `p95 > 0.85×limit` → recommend `ceil(p95 × 1.3)`; reclaim if
`p95 < 0.5×request` → `ceil(p95 × 1.3)`; never reclaim a Guaranteed (request==limit) workload (would change
its QoS class). *Why:* compares MEASURED sustained usage to the workload's OWN declared request/limit
(borrowed normativity), and only ever recommends — a human applies it.

---

## Lane 6 — Determinism, coverage & the test gates

**27. Content hash (SHA-256).** The graph version is `sha256(raw merged-KG bytes)`; a release pins this and
hard-fails if the loaded graph's hash differs. *Why:* the knowledge graph ships as an immutable,
hash-pinned release — the tamper/drift check every finding is stamped against.

**28. Replay digest (SHA-256).** Each tick's `{evalNow, fingerprints, findings, cascades, unexplained}` is
canonically JSON-marshalled and SHA-256'd; the live digest must equal the replay digest. *Why:* this
equality **is** the byte-identical-replay guarantee — same readings + same graph ⇒ same findings, provably.

**29. Resolvability.** `config-bound bars / config-eligible bars`. *Why:* the honest "how much of what could
carry an operator-declared early-warning bar actually does" — the 59% you saw.

**30. Silence-ledger identity.** By construction `total pairs = watched + silent`, every (entity,variable)
pair in exactly one bucket. *Why:* a reconcile-by-construction ledger of what Vigil is blind to — gaps are
accounted, never blank.

**31. The gate metrics (how the scorecard numbers are computed).**
- **Band coverage** = `#{realized ∈ [lo,hi]} / N`; must land in [0.65, 0.98] (nominal 0.8). Under-coverage =
  overconfident bands = the trust-killer, so the low side is tighter.
- **Event recall** = `events-warned / events`, warned = got ≥8 steps (~2 min) of lead. Floor 1.0.
- **False-warning rate** = `false-warnings / full-obs candidates`; cap 0.30.
- **In-band crossing accuracy** = `crossings-in-band / warned`; floor 0.8 (the band is the promise, not the
  point).
- **Cross-service**: root accuracy = `Σ root-correct / Σ fired-ticks`; caller recall = avg `|hit∩truth|/|truth|`;
  caller precision = avg `|hit∩truth|/|named|`; false-cascade count over expected-silent scenarios. Floors are
  **exact 1.0 / 0** because naming the wrong root or a phantom caller is a *bug*, not statistical noise.
- A thin corpus is always **INSUFFICIENT, never a pass** — a class ships on evidence, not its absence.

---

## Why this whole philosophy
- **Simple deterministic core** → byte-identical replay → an auditable, reproducible engine (you can prove
  what it said and why).
- **Statistics kept off the digest** → CUSUM/Pearson/TimesFM *annotate* but never *decide*, so a model
  hiccup can't corrupt detection.
- **Declared constants, never data-fit** → Vigil never invents a threshold; every cutoff is a stated value
  you can inspect and version.
- **Robust estimators (MAD medians, detrending, permutation nulls)** → it resists being fooled by the very
  spikes and ramps it's hunting.
- **Mandatory uncertainty bands + auditable silence** → it never gives false precision and always says why
  it stayed quiet.
