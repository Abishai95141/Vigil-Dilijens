# Phase 2 — Marquee Forecasting: working plan & decision log

Started 2026-06-12. Owning docs: 09 (layer), 06 §3.3/M5 (Tier-B), 10 M5 (surface),
11 M5 (backtest gate), 14 A13/A15, 00 §5.2 (the five interaction points),
techstack §5 (Go↔clockd = plain gRPC; grpc.aio server; stateless; health feeds
the degradation panel). Readiness baseline: `artifacts/forecasting-readiness.md`.

## 1. End goal of the phase (the vision, restated against doc 00)

Add the SECOND pillar — "what crosses a bar SOON" — without ever compromising the
first. The clock answers exactly one question (when does this number cross that
number); the graph supplies all meaning (precursor tags), normativity (bars),
scope (blast radius), and selection (Tier-B). The join happens at surfacing only.
An operator gets minutes-to-hours of warning, phrased "projected to cross", with
a band that never collapses to a line — and NO warning ships before its backtest
calibration gate passes (the trust-first rule; mis-calibrated bands are the
risk-register item this phase exists to defuse).

## 2. Functional requirements (from docs 09/06/10/11/14)

R1. Model-agnostic clock behind the frozen no-strings proto; any conformant model
    is a drop-in; swap touches zero knowledge (09 §3.1).
R2. Graph→model flows are EXHAUSTIVELY two: target selection; footprint
    subtraction (Phase 3). Model input is bare floats. Model output never enters
    the graph (00 §5.2 negative space).
R3. Non-gating absolute: detection identical whether clockd is present, degraded,
    or absent; degradation is VISIBLE (14 A13 health panel), never silent.
R4. Eligibility funnel (09 §3.2): Tier-B entities (06) × signals that are bound,
    gauge-bearing (seriesshape — built), regularly sampled, showing real
    dynamics, carrying a RESOLVABLE bar, preferring T0- precursors.
R5. Tier-B budget: configured invocation ceiling per cycle (param exists, dev 50);
    ranked candidates; "eligible but unbudgeted" PUBLISHED (06 §3.3/M5).
R6. Emission guardrails (09 §3.6): silence is the default. Hard silences: flat/
    low-variance series, too-wide bands, no crossing within horizon. A candidate
    carries: target ref, projected crossing + band, qualitative confidence class,
    REFERENCES to authored precursor edge + blast radius, is_projection mark.
    Never a generated causal sentence.
R7. Horizon envelope stated per target class (~4–8h max at 15–30s cadence),
    enforced at emission (09 §3.7).
R8. Backtest gate (11 §3.5) per target class BEFORE operator visibility: band
    coverage, time-to-cross error, silence correctness, horizon suitability.
    Clock swap/upgrade re-runs every shipped class's gate.
R9. Surface (10 M5): PROJECTED-class cards + predictive topology marks in a
    SEPARATE visual language from current marks; band always rendered; register
    audit clean ("projected to cross", never "will").

## 3. Success criteria (exit gates I will hold work against)

- 09 M1: conformance fixtures pass; stub↔model swap test proves knowledge untouched.
- 09 M2: on replay fixtures, candidates emit ONLY under §3.6 conditions.
- 09 M3 🔒: working-set→OOM class passes band-coverage + time-to-cross gates
  before ANY operator-visible warning.
- 09 M4 + 10 M5: live warning with blast radius; charter/register audit clean.
- 06 M5: ceiling respected; unbudgeted-eligible list published.

## 4. Architecture decisions (decision log)

D1. **Python stubs committed** (techstack §2.6) — buf generates Python into
    `clockd/src/clockd/gen/` alongside Go in `proto/gen/go/`; clockd stays
    self-contained, base env still light (stubs need `grpcio` only at SERVE time
    — generated message code needs protobuf runtime; serving deps live behind an
    extra so `just clockd-test` stays hermetic).
D2. **Validate-the-model-first**: before building the adapter, install the pinned
    package in a scratch env, confirm `TimesFM_2p5_200M_torch` exists in the
    PyPI dist, run CPU inference on synthetic series, measure latency, verify
    the quantile head shape. If PyPI lacks the 2.5 class → pin git revision
    (uv supports it). Findings recorded below (§6).
D3. **Go client owns non-gating**: obsd calls clockd with a short deadline; any
    error/timeout ⇒ clock-degraded state surfaced on the Tier-B panel; the tick
    NEVER waits beyond the budget. The deterministic digest is untouched by
    anything the clock returns (PROJECTED is off the byte-replay digest — the
    audit confirmed the digest covers the deterministic core only).
D4. **Forecast pipeline runs in obsd** (eligibility, context fetch, projection
    against bars, guardrails, emission) — clockd stays a dumb stateless function
    over floats. This keeps both permitted graph→model flows inside one audited
    Go package (`internal/clock` + a new `internal/forecast`).
D5. **Context fetch v1** = hot ring (1h @ 15s = 240 points) only. The marquee
    OOM class targets hours-scale creep; 240 recent points suffice for v1 and
    avoid the warm-scan design until the backtest gate demands longer context.
    Revisit with M3 evidence (recorded as an open risk, not silently capped —
    the candidate's decomposition record will state context length).

## 5. Milestone sequencing for the build

1. 09 M1: buf python gen → clockd grpc.aio serving (StubClock) → TimesFM adapter
   behind `model` extra (pins per §6) → Go client + bufconn tests → conformance
   fixtures BOTH sides (band-never-collapses, lengths, flat-input honesty) →
   swap test (same fixtures pass against stub AND model adapters).
2. 06 M5 + 09 M2: Tier-B funnel in selection (entity level) × signal funnel in
   `internal/forecast` (eligibility, dynamics check, bar resolution, budget
   ranking, unbudgeted list) → projection + guardrails → candidates on replay
   fixtures only.
3. 11 M5 + 09 M3: harness backtest (band coverage, time-to-cross, silences) on
   captured bundles; gate for the working-set class.
4. 09 M4 + 10 M5: /api/warnings + web cards + predictive topology marks behind
   the gate; A13 degradation panel; register audit.

## 6. Validation findings (running log)

**2026-06-12 — TimesFM 2.5 validated on CPU (D2 closed, probe under /tmp/tfm-probe):**
- PyPI `timesfm==2.0.1` SHIPS `TimesFM_2p5_200M_torch` (no git pin needed);
  resolved with `torch==2.12.0`, `numpy==2.4.6`, Python 3.12.
- **Pins adopted (A15)**: `timesfm==2.0.1` + checkpoint
  `google/timesfm-2.5-200m-pytorch` @ revision
  `1d952420fba87f3c6dee4f240de0f1a0fbc790e3`.
- Quantile head: output shape (batch, horizon, 10) = mean + deciles q10…q90
  (`use_continuous_quantile_head=True`, `fix_quantile_crossing=True`).
- **CPU latency ~288 ms/forecast** (240-pt context, horizon 64, Apple Silicon):
  the dev Tier-B budget of 50 invocations ≈ 14.4 s/cycle — fits a forecast
  cadence ≥30 s (the warm path is parallel and never gates the 15 s tick).
- **Honesty checks all pass**: flat input → flat point (149.93→149.93) with
  narrow non-collapsing band; ramp → trend continued (200→226) with band
  widening 5.9→12.1; sawtooth → no fabricated trend; `band_collapses=false`
  on every shape. Model load 4.2 s warm (one-time per process).

D6. **Sawtooth context is the OOM class's hard case** (M3 evidence A,
    2026-06-13): with prior ramp→kill→reset cycles in context, the zero-shot
    clock projects the next RESET, not the bar crossing (recall 0.022 on the
    fast-cycling corpus) — while staying honest (band coverage 0.845, zero
    false warnings). Doc-anticipated (09 §3.4 context pollution); remedy =
    Phase 3 splice points at known boundaries (container restarts — already
    identity-layer knowledge). Gate consequence: the class ships (if at all)
    on the REPRESENTATIVE slow-creep corpus; the sawtooth limitation is
    recorded and drives 09 M5's requirements. Full numbers:
    corpus/labels/forecast-gate-09M3.md.

## 7. Risks being tracked

- PyPI 2.0.x may not ship the 2.5 class → git-revision pin fallback (D2).
- Checkpoint download ~1GB; cache via HF_HOME; never a CI dependency.
- CPU latency vs 50-invocation budget — measure in M1, enforce in M5.
- Band calibration on cluster-shaped series (sawtooth GC, flat-then-spike) is
  THE phase risk; nothing ships before M3's gate (R8).
- kind realism for node-pressure backtests — container-scoped marquee class is
  real on kind; staging k3s remains the stated Phase-1 line item.
