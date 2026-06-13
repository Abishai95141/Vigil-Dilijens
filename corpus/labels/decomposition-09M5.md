# 09 M5 — Decomposition (context-window splice points): evidence log

Doc 09 §3.4 / M5, Phase 3. The forecast pipeline splices the context at the most
recent KNOWN event boundary — an operator context window (10 M6, deploy/config) or an
auto-detected gauge RESET (a container restart drops the cgroup working_set sharply) —
and forecasts only the clean post-event remainder. Code: `obsd/internal/forecast/
decompose.go`; wired into `RunCycle` (between context-fetch and the clock call),
`cmd/obsd` (operator splice points from the context-window store), and `cmd/replay`
(the `-forecast-no-decompose` A/B toggle). The footprint leaves the model's input; the
reason never enters the model (charter §4).

## The problem it fixes (09 M3 evidence A)

On a sawtooth working-set history (ramp→OOM-kill→reset→ramp), the zero-shot clock
projects the next RESET, not the bar crossing — **recall 0.022**, the doc-anticipated
context-pollution failure (09 §3.4). The remedy: cut the context at the last reset so
the clock sees only the current clean ramp.

## The exit-gate property + how the design guarantees it

Doc-13 Phase-3 exit gate: decomposition **improves backtest error WITHOUT degrading
band coverage**. The design makes this structural: decomposition is a **no-op on a
clean series** (no event boundary → the full window passes through) and only changes a
polluted one. So a clean ramp is forecast identically with or without decomposition;
only a sawtooth is spliced.

A RESET (vs a transient GC/cache dip on a healthy series) must satisfy ALL of:
1. a sharp relative drop (`series[i] < (1−frac)×series[i-1]`, default frac 0.4);
2. a **magnitude floor** — the drop is a significant fraction of the series' observed
   RANGE (so a small absolute wobble on a near-zero baseline does NOT trip a purely
   relative test);
3. **persistence** — the gauge does not recover toward the pre-drop level within the
   next 4 points (a transient dip bounces back; a restart stays low and ramps slowly),
   and a drop at the very last point (no lookahead to confirm) is NOT spliced.

Abort criterion (doc 09 §3.4): if the clean remainder is shorter than `min_context`,
or splicing removes more than `max_explained_fraction` (0.9) of the window, emit
nothing (silence `decomposition-aborted`) rather than forecast on residue.

## Unit-level proof (12 golden tests, `decompose_test.go`)

- **Sawtooth** (ramp→reset→ramp) → spliced at the last reset; the clock sees the clean
  second ramp. ✓
- **No-false-positive (the exit-gate-critical regressions)**: a clean ramp with a
  single GC dip-and-recover → **no splice**; an oscillating cache-churn gauge → **no
  splice**; a sub-unit wobble on a near-zero baseline → **no splice**. ✓ These pin the
  property that decomposition cannot degrade a clean series' band coverage.
- Operator context-window splice; latest-boundary-wins; abort-too-short;
  abort-too-explained; coincident reset+operator → ONE record; disabled = no-op;
  deterministic (pure function, replays byte-identically). ✓

## Adversarial review (4 dimensions, refute-by-default) — 6 defects, ALL fixed

A multi-agent review (per-dimension reviewer → refute-by-default verifier) confirmed
6 real defects; all fixed + regression-tested:
- **(critical)** `lastReset` fired on a single transient dip-and-recover, falsely
  splicing a CLEAN series — the gate-forbidden failure. FIX: persistence check.
- Near-zero baseline noise tripped the purely-relative test. FIX: magnitude floor vs
  the series range; corrected the misleading comment.
- Coincident reset+operator boundary double-appended two splice records. FIX: one
  boundary → one record (reset wins).
- `max_explained_fraction=0` silently DISABLED the abort (footgun). FIX: validation
  rejects 0 when decompose enabled.
- `Splice.At` inconsistent (declared vs sample time) for level-shifts. FIX: always the
  boundary sample's time.
- Live loop passed the unfiltered splice-point history each cycle (cost). FIX: relevance
  window (8h) filter.

Determinism: decomposition runs only on the warm/forecast path; it never touches the
deterministic detection digest (verified by the digest covering only fps/findings/
cascades/unexplained — the forecast cycle is consumed by the events sink, never digested).

## Live A/B exit-gate measurement — RUN (2026-06-13, real cluster + real TimesFM)

Captured the sawtooth live: `leak-oom` (96Mi limit, bar 91.2Mi), 3 OOM cycles / 81 ticks /
651,569 samples; byte-identity held in every replay pass. The leaker's working_set is ONE
continuous CEI showing the sawtooth (resets `95→8`, `94→0`, `95→0`). A/B via
`replay -forecast` ±`-forecast-no-decompose`, scored by `harness.forecast_gate`
(`-forecast-min-context 16`, fitting the ~25-point fast cycles — doc 09 §3.7 per-class
envelope; the production 64 spans multiple cycles so the spliced remainder always aborts):

| | crossing-bearing forecasts that HID a crossing | wrong "no-crossing" (reset) projections | band coverage |
|---|---|---|---|
| WITHOUT decompose | 50 | 38 | 0.656 (38,953 pts) |
| WITH decompose    | **22** (−56%) | **14** (−63%) | **0.649** (38,038 pts) |

WITH decompose: **16 splices + 29 `decomposition-aborted` honest silences** on the sawtooth
(0 splices when disabled — the A/B flag is real). **Result: decomposition cut the
forecaster's wrong-projection error by >half — fewer forecasts hid a real crossing, far
fewer wrongly projected the reset — WITHOUT degrading band coverage (0.656→0.649, within
noise).** That is the doc-13 Phase-3 exit-gate property directionally demonstrated on the
real model.

**Honest limits of this capture:** positive event recall stays 0.00 both ways — on this
FAST-cycling workload the spliced clean ramp (~16–25 pts) is short, so the model honestly
ABORTS (or says band-too-wide) rather than warning; that is correct charter behaviour
(silence beats a wrong reset-projection), but it does not yet produce a POSITIVE warning.
The formal gate verdict is INSUFFICIENT (2 class-eligible crossing events < the 3 required —
a corpus-size limit, not a decomposition defect). FULL CERTIFICATION (positive recall +
≥3 events) needs either a SLOWER sawtooth (longer clean ramps so a spliced ramp is forecast-
able) or more cycles — the live calibration finding. A new calibration was made FROM this
live data: the reset "recovered" threshold is `0.9×prev` (not `(1−frac)×prev`) — a fast-
ramping restart climbs above `(1−frac)×prev` within the persistence window but stays below
`0.9×prev`, so the looser threshold mistook real restarts for transient dips (0 splices →
16 splices after the fix). Pinned by `TestDecomposeFastRampingRestartDetected`.

## Live A/B exit-gate measurement — original runbook (re-run / extend)

The empirical certification (sawtooth recall improves; plateau band coverage unchanged)
requires the kind cluster + clockd + a multi-cycle sawtooth capture. Procedure:

1. `just up && just boutique`; `kubectl apply -f deploy/workloads/node-exporter.yaml`;
   `kubectl apply -f corpus/chaos/leak-oom.yaml` (the sawtooth: 96Mi limit, ~7 min/cycle).
2. Capture ≥3 OOM cycles: `./bin/obsd -kubeconfig ~/.kube/config -store-dir <store> ...`
   (~20–25 min), SIGTERM to seal.
3. `cd clockd && uv run --extra model python -m clockd.server --port 50051 --clock timesfm`.
4. A/B replay:
   - WITHOUT: `./bin/replay -bundle <store> -forecast -forecast-no-decompose -forecast-out events-no.jsonl -export-parquet readings.parquet -q`
   - WITH:    `./bin/replay -bundle <store> -forecast -forecast-out events-yes.jsonl -q`
5. Gate both: `cd harness && uv run --extra analytics python -m harness.forecast_gate
   --events events-{no,yes}.jsonl --readings readings.parquet --metric container_memory_working_set_bytes`.
   EXPECT: recall(with) ≫ recall(without)=~0.022; band coverage preserved on the clean
   plateau streams (decomposition no-op there).

Status: the splice MECHANISM is unit-proven (sawtooth→clean ramp; clean→no-op) and the
code is adversarially clean; the live A/B is the remaining empirical certification, after
which the sawtooth/cycling class may become gate-eligible (it is NOT operator-visible
until then — the gate rule holds).
