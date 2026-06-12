# Phase-2 (Forecasting Layer, doc 09) — readiness record

Compiled 2026-06-12 during the pre-Phase-2 audit. Sources: doc 09, doc 14 (A13/A15,
§2), doc 06, techstack §5; live repo inventory; web research on TimesFM packaging.

## A. What doc 09 M1–M4 require

| Milestone | Requires | Exit |
|---|---|---|
| M1 clock interface + reference adapter | Frozen proto contract; TimesFM 2.5 behind it; stub swap test | conformance fixtures pass; swap proves knowledge untouched |
| M2 eligibility funnel + projection | Tier-B entities (06); signal funnel (§3.2); resolved bars (04); context windows from the store (05); guardrails/silences | candidates emit only under §3.6 on replay fixtures |
| M3 backtest calibration gate | Harness (11): band coverage + time-to-cross error per class on recorded series | working-set→OOM passes BEFORE any operator sees a warning |
| M4 marquee warnings live | 10 M5 early-warning surface; blast radius (07) attached | charter language audit on every surfaced string |

## B. Asset inventory (verified this session)

- **Proto contract** `proto/vigil/clock/v1/clock.proto`: Forecast (series, horizon,
  quantiles, covariate_future) + Health (ready, status_code). **No string fields**,
  conformance-tested. Sufficient for M1–M2; bars/eligibility/horizons live obsd-side
  by design (projection against the bar happens in Go, the model never sees it).
- **clockd**: StubClock (pure stdlib, honest flat forecast + widening band, covariate
  pathway) + tests. gRPC serving = explicit Phase-2 placeholder (`server.py` raises).
  `model` extra empty, awaiting pins (A15).
- **obsd/internal/clock**: doc.go contract + proto conformance test. No client yet (M1).
- **Selection**: Tier-B structurally present, empty until Phase 2.
  `selection.tier_b_budget_per_cycle` param EXISTS and is validated (>0); dev 50.
- **Store**: warm tier 2h segments / 7d retention exists (built for forecast context,
  doc 14 §2.1). Hot ring 1h.
- **Funnel inputs (NEW this session)**: `internal/seriesshape` canonical data_type
  parser, shared by the loader (`graph.Signal.Shape()`, `Forecastable()`) and
  graphlint. **Measured funnel on the real KG: 457 Metric → 434 series →
  409 gauge-or-counter → 203 contain-a-gauge** (doc 09 §3.2 estimated 457/415/390/203
  — contains-gauge matches exactly). Curation queue shrunk from "112 variants" to
  **8 unclassifiable spellings + 15 Metric-modality/payload-typed disagreements**,
  both listed by id in the graphlint gap report.

## C. External facts (TimesFM packaging, A15)

- PyPI package `timesfm` latest **2.0.1 (released 2026-06-08)** — the package version
  (2.x) ≠ checkpoint version (2.5), exactly the A15 confusion; pin BOTH.
- Checkpoint: HF `google/timesfm-2.5-200m-pytorch` (third open checkpoint, 200M
  decoder-only, native quantile head, max context 16,384). Pin by revision hash at
  adoption time.
- Loading: `timesfm.TimesFM_2p5_200M_torch.from_pretrained(...)`; `[torch]` extra;
  CPU inference is fine for dev (A15); XReg covariate support present for 2.5.
- clockd pins Python >=3.12,<3.13 — torch wheels fine.
- VERIFY AT PIN TIME: that the PyPI 2.0.x dist actually ships `TimesFM_2p5_200M_torch`
  (the GitHub README at one point said "pip install coming soon — use git clone");
  if not, pin the git revision in uv instead. Either way the pin lands behind the
  `model` extra so the base env stays light.

## D. Gap analysis

**Prep that is DONE (this session):** data_type normalization (shared parser + measured
funnel + curation queue named); Tier-B budget param (pre-existed, verified); A15
packaging research recorded here.

**Phase-2 proper (do NOT pre-build):** clockd gRPC serving + stubs; Go clock client;
TimesFM adapter + pins; eligibility funnel; projection + guardrails; backtest gate;
10 M5 surface; 06 M5 budgeting; A13 degradation panel.

**Named design items to settle early in Phase 2 (not blockers):**
1. **Context fetch path**: warm segments have no per-stream read API (replay-only
   scans). Design: ONE backward scan per eval cycle over the last K segments,
   demuxing context windows for the whole Tier-B batch (50 targets) — NOT a
   per-target scan (which would be ~GBs/cycle). v1 marquee class needs ~1–2h
   context (hot ring + 1–2 segments), nowhere near the 16k ceiling.
2. **Forecast determinism**: PROJECTED output is NOT under the byte-replay digest
   (the digest covers the deterministic core only — confirmed in this audit).
   Replay of warnings = re-running the clock on identical context; backtest
   reproducibility wants pinned model revision + torch determinism flags; the
   charter does not demand byte-identity for PROJECTED.
3. **Precursor tags**: T0- vocabulary exists in the graph (12 temporal tags in use);
   the funnel's "preferably T0- precursor" gate reads it directly.

## E. Ranked blockers → mitigations

1. ~~data_type normalization~~ — CLOSED this session.
2. PyPI 2.5-class availability uncertainty — verify at pin time; git-revision pin
   fallback (uv supports it).
3. Model download (~1GB) in dev loop — cache in uv env / HF_HOME; never in CI base.
4. CPU latency per inference within the 50/cycle budget — measure in M1 conformance
   (stub swap test gives the harness for free); budget param already enforceable.
5. kind node-pressure realism for backtest corpus — staging k3s VMs already a
   stated Phase-1 line item (doc 14 §3.1); container-scoped classes (the marquee
   working-set→OOM) are real on kind.
