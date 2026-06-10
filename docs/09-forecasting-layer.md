# 09 — Forecasting Layer

**Role in suite:** Pillar 2, the "soon" half of the runtime. A narrow early-warning layer built on one discipline: **the clock is the model, the reasons are the graph, the join is the surfacing step — never merged.** The model answers exactly one question — when does this number cross that number — and everything that makes the answer *mean* something is authored knowledge.

---

## 1. Purpose and ownership

This document owns: the model-agnostic clock interface and swap contract; the signal-level eligibility funnel and its intersection with entity selection; the per-target pipeline (fetch → decompose → forecast → covariate → recombine → project → emit); decomposition mechanics and abort criteria; covariate admission rules; emission guardrails; the horizon envelope; and the calibration gate that stands between this layer and any operator-visible warning.

It does not own: the precursor tags, relation edges, and bars it consumes (02/04), entity selection (06), blast-radius derivation (07), the backtest harness that gates it (11), or how warnings render (10).

## 2. System context

Learned statistical baselines were removed from this architecture because they do not generalize across customers. One narrow form of prediction earns its way back: a pretrained, **zero-shot**, univariate forecaster whose input normalization strips scale and units before the network sees a series, making it schema- and scale-independent with nothing learned per customer. It forecasts the *shape* of whatever series it is handed — and on truly random input it correctly goes flat, which is the honest behaviour. The layer runs in parallel to detection and **never gates it**: the deterministic path is identical whether forecasting is present, degraded, or absent. Without this layer the system is silent until a bar is actually crossed; with it, an operator gets minutes-to-hours of warning plus a graph-scoped blast radius — and the model still never says a failure *will* occur, only when a number is projected to cross a number.

## 3. Core mechanics

### 3.1 The clock interface (model swap contract)

| Aspect | Contract |
|---|---|
| Input | One bare float series (scale-normalized inside the clock); optionally one known-future covariate channel, kept outside the main inference path (§3.5) |
| Forbidden input | Labels, names, units, entities, graph structure, reasons — anything semantic |
| Output | Point forecast plus a quantile band over the horizon |
| Properties required | Zero-shot (no per-customer training); univariate; forward arrow of time enforced; correlational and declared so |
| Reference implementation | TimesFM 2.5 — a 200M-parameter decoder-only pretrained transformer with reversible instance normalization for scale independence, long context, and a native quantile head |

Any conformant model is a drop-in: the contract is the architecture, the model is a part. Swapping the clock touches zero knowledge.

### 3.2 Target eligibility: the signal funnel

Applied to signals **on the entities Tier-B selection (06) has already chosen** — the entity funnel picks where to look, this funnel picks what to run:

```
589  all signals in the ontology
457  modality = Metric            (logs/events/state/traces/profiles/audit out — not series)
415  regularly sampled            (watch / event-driven / streaming out)
390  gauge- or counter-typed      (forecastable in principle; counters only via rate derivation, flagged)
203  contain a gauge              (cleanest fit — a level you project directly)
```

A target must additionally be: **bound** (04), showing **real dynamics** (flat series are skipped, §3.6), carrying a **resolvable bar** (no bar ⇒ nothing to cross ⇒ ineligible, listed as unbounded per 04 §3.4), and **preferably a `T0-` precursor** — only a precursor's crossing carries early-warning meaning. The Tier-B set is the intersection: precursors that matter, on entities that matter, within the invocation budget.

### 3.3 The per-target pipeline (off the hot path)

1. **Fetch** the recent context window from the store (05).
2. **Decompose** (§3.4): subtract graph-explained event footprints; **abort** the target this cycle if too much of the window is explained away.
3. **Forecast**: clock inference; point plus quantile band; scale handled inside the clock.
4. **Covariate** (optional, §3.5): known-future regressors only.
5. **Recombine**: add back a footprint only if a *future* occurrence of its event is actually scheduled.
6. **Project** against the resolved bar from the graph: time-to-cross point estimate plus an `[earliest, latest]` band from the quantiles.
7. **Emit** an early-warning candidate **only if** the projection crosses within the horizon with adequate confidence; otherwise emit nothing. Silence is the default output.

### 3.4 Decomposition — pull the footprint, not the reason

A spike with a known cause (a deploy at 10:02) is a one-off the clock cannot predict, and feeding it the past spike only pollutes the forecast. The graph — primarily via operator-declared **context windows** (10), which carry scope and timestamps — identifies the spike's **numeric footprint**; the pipeline subtracts it; the clock forecasts the clean remainder; the footprint is re-added only where a future occurrence is scheduled. The phrasing is exact and charter-bound: the **footprint** leaves the model's input; the **reason** never enters the model and remains in the graph as the human-facing explanation. Footprint shapes are modelled per event class (level shift, transient, ramp), and a per-target abort criterion guards trust: if the explained fraction of the window exceeds the limit, the forecast aborts as untrustworthy rather than emitting on residue.

### 3.5 Covariates — known-future only

The clock may accept one auxiliary regressor through a separate linear pathway that asserts no causation. Admission rules: the covariate's **future values must be genuinely knowable** (a scheduled job, a calendar); past-only drivers are ineligible, however correlated; prefer covariates easier to know than the target. Covariates **sharpen the clock; they never produce reasons** — no covariate is ever surfaced as an explanation.

### 3.6 Guardrails and emission

A candidate that survives to emission carries **only**: the target reference; the projected crossing and band; the qualitative confidence class derived from band width; **references** to the authored precursor edge and the blast-radius entity set (derived by 07's downstream walk) — never a generated causal sentence. Mandatory marks: `is_projection`; band never collapsed to a line. Hard silences: flat or low-variance series; bands too wide to be useful; projections that do not cross within the horizon. Forecast findings enter evidence tagged as projections, never as measured facts, and the layer's availability has zero effect on detection.

### 3.7 The horizon envelope (stated, not implied)

Honesty about reach: with scrape cadences of 15–30 seconds and a quantile horizon on the order of a thousand steps, the maximum meaningful early-warning horizon is roughly **four to eight hours**, with the long context window covering days of history. This layer is built for the hours-scale creep — the working-set climb toward an OOM — and is structurally incapable of "tomorrow." The envelope is published per target class and enforced at emission.

### 3.8 Worked example — the OOM case with blast radius

A pod with a 512 MiB limit (bar ≈ 486 MiB from its own config, via 04) has crept to 420 MiB by 10:00. **Clock:** the model receives only the bare series and projects a crossing of 486 at ~10:13, band 10:09–10:22. **Reasons:** the graph records working-set as a `T0-` precursor to the OOM-kill phenomenon, and its relation edges record that an OOM on this pod stresses its node (first-order) and can evict siblings (second-order). **Join (at surfacing):** "memory projected to cross its configured limit in ~13 min (band 9–22); known OOM precursor per the graph; blast radius per the graph: node X and the siblings on it — heads-up." A candidate with a band, never a verdict.

### 3.9 Conceptual structures

| Structure | Fields (meaning) |
|---|---|
| Forecast target | Entity CEI; variable; series kind (gauge / counter-rate, flagged / histogram-quantile); bar reference with source provenance; precursor flag |
| Early-warning candidate | Target; projection (bar value, time-to-cross point, `[earliest, latest]` band); confidence class; authored phenomenon-edge reference; blast-radius CEIs (authored-walk-derived); `is_projection`; decomposition record (footprints removed/re-added, explained fraction) |

## 4. Epistemic discipline enforcement

Output is PROJECTED-class with AUTHORED references attached, never fused — the candidate carries pointers to edges, not generated "why" text. The two permitted graph→model flows (target selection; footprint subtraction) are the exhaustive list from the charter; the model's inference channel sees bare floats only. The graph never ingests anything this layer produces. The non-gating rule and the silence defaults are charter enforcement in mechanism form.

## 5. Structural dependencies

**Consumes:** precursor tags, relation edges, and threshold rules (02); stable target identity (03); bindings and resolved bars (04); context windows of series (05); the Tier-B entity set and budget (06); blast-radius walks (07); operator context windows as splice points (10). **Provides:** early-warning candidates to surfacing (10). **Gated by:** the backtest suite (11) — no warning class ships before its calibration gate passes.

## 6. Failure modes and honesty mechanisms

Mis-calibrated bands (quantiles not matching reality on cluster-shaped series — sawtooth GC, scaling steps, flat-then-spike) — countered by the per-target-class backtest gate before exposure, and band-coverage regression thereafter. Bad splices polluting input — countered by per-event-class footprint models and the abort criterion. Counter-rate artifacts (resets, derivation noise) — counters are flagged second-class targets; gauges first. Warning fatigue — countered by the silence defaults, the horizon cutoff, and confidence classes. A wrong bar (config drift) — countered by bar provenance with resolved-at stamps and re-binding (04). Model regressions on swap or upgrade — countered by the clock contract plus mandatory backtest re-gating per 11.

## 7. R&D execution sequence

1. **M1 — Clock interface + reference adapter** (Phase 2). Contract frozen; reference model behind it. Exit: conformance fixtures pass; swap test with a stub clock proves knowledge untouched.
2. **M2 — Eligibility funnel + projection** (Phase 2). Funnel over Tier-B; projection against resolved bars; guardrails and silences. Exit: candidates emit only under §3.6 conditions on replay fixtures.
3. **M3 — Backtest calibration gate** (Phase 2 exit, with 11). Band coverage and time-to-cross error per target class on recorded series. Exit: **the first shipped class (working-set → OOM) passes its gate before any operator sees a warning.**
4. **M4 — Marquee warnings live** (Phase 2). A small set of high-value precursors, blast radius attached. Exit: charter language audit on every surfaced string.
5. **M5 — Decomposition** (Phase 3). Context-window splice points; per-event-class footprints; abort criterion. Exit: footprint removal improves backtest error without degrading band coverage.
6. **M6 — Known-future covariates** (Phase 4). Admission rules enforced; scheduled-job and calendar regressors first. Exit: covariate targets beat their univariate baselines in backtests.
7. **M7 — In-context refinement (optional)** (Phase 5). Inference-time example conditioning, **hard-gated on a confirmed loadable checkpoint for the deployed model line** — at blueprint time the in-context variant exists on the base model line, not the deployed one, so this milestone does not start until that gate clears.

## 8. Open questions owned here

Surfacing thresholds — how wide a band and how short a horizon are still worth showing (answered by the backtest harness, not intuition); horizon and cadence defaults per target class; which graph events beyond operator context windows carry timestamps clean enough to serve as splice points (the question that most gates Phase 3); whether histogram-quantile targets justify their derivation complexity in v1.
