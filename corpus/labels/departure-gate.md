# departure gate — evidence (doc 15 cap. C — the band-departure anomaly)

**Verdict: PASSED** (`just departure-gate`, exit 0) — 2026-06-16.

## What it certifies

The "anomaly" half of Capability C, SALVAGED from a KILLED shortcut (doc 15 §3 — "anomaly =
band-departure surfaced MEASURED in the unexplained channel"; that died because a PROJECTED
band edge in the replay digest breaks byte-replay AND calling the comparison MEASURED launders
PROJECTED→MEASURED). It survives **only off-digest, classed PROJECTED**: a MEASURED sample that
left the PROJECTED band its OWN recent forecast drew for that time — the series did something
its own forecast did not anticipate.

Each scenario folds the **REAL** `departure.Detect` (a pure function of observations + params)
against a **LABEL ORACLE**. The flag is model-derived (the band is the clock's forecast,
PROJECTED by class), so the gate certifies the **JOIN**, not the tick-digest — its determinism
is the honest, narrower one ("same recorded band + sample + params ⇒ same flags"); the Go drift
guard proves the frozen corpus reproduces byte-identically.

## Floors (all green)

| Floor | Result |
|---|---|
| **FP-ON-DECOYS == 0** (CARDINAL) | 0 — every decoy/healthy scenario produces ZERO departures: a noisy-but-stationary series (whose clock band is WIDE) absorbs its noise; a wide-band bump is absorbed; a near-miss within the structural margin is absorbed; an on-edge sample is inside. The anti-false-warning floor — the whole point of the salvage |
| **RECALL** | OK — every true-step scenario (above and below) fires a departure |
| **SIDE-FIDELITY** | OK — a firing departure's side (above / below) matches the oracle |
| **PROJECTED-CLASS** | OK — every departure is classed PROJECTED (band ⋈ measured), never a standalone MEASURED anomaly score (no class laundering) |
| **CHARTER == 0** | 0 — no causal claim, no "anomaly score" language; the system never asserts the departure as a measured fact or a cause |

## Scenarios (corpus/departure/, 8 total — the near-miss/decoy corpus, task #75)

- `step-above` (band [10,20], realized 50 → departure above) · `step-below` (band [40,60],
  realized 5 → departure below) — **recall**.
- `noisy-decoy` (wide band [0,40], noisy 35 inside → silent) — **THE CARDINAL FP DEFENSE**.
- `wide-band-absorbs` (very wide band [5,95], bump 80 absorbed → silent — the detector never
  out-claims the forecast's own confidence) · `near-miss-margin` (sample 20.8 a hair past a
  tight band, absorbed by the structural margin → silent) · `healthy-inside` · `edge-inside`
  (on the upper edge is inside, strict) — all silent.
- `zero-width-band` (COLLAPSED band [5,5], far-away sample 50 → silent) — a band must NEVER
  collapse to a line (doc 01); a degenerate forecast is skipped, never compared, so the
  structural margin can never become a zero-ULP hair-trigger (degrade-never-fabricate at the
  model-adjacent edge).

## Machinery

- **Core:** `obsd/internal/departure/departure.go` — `Detect` flags a realized sample beyond its
  band edge (past a STRUCTURAL margin = a fraction of the band WIDTH, not a learned threshold).
  The band IS the bar; the detector adds no threshold of its own — a noisy series's WIDE clock
  band is the FP defense. A malformed band (non-finite, upper < lower) NEVER fabricates a departure.
- Regen: `REGEN_DEPARTURE_CORPUS=1 go test ./obsd/internal/departure -run RegenDepartureCorpus`.
- Always-on guards: `TestDepartureCorpusFrozenConsistent` (determinism) + 7 unit tests (step
  above/below, noisy-decoy-inside, edge-inside, margin-absorbs-near-miss, malformed-band/
  zero-width-never-fires, determinism). Scorer: `harness/src/harness/departure_gate.py`;
  7 python regression tests. The gate recipe runs `go test -race -count=1` (the determinism
  guard never caches).
- Surface: `/api/departures` (`api/departure.go`, `BuildDepartures` — OFF / gate-pending / quiet
  / active states). Wired behind `--departure-enabled` (`phaseCDepartureGatePassed=false`,
  gate-pending). Recipe: `just departure-gate`.

## LIVE-WIRED on kind-vigil (2026-06-16) — the gate-pending posture

`obsd --departure-enabled`: `/api/departures` serves `enabled: true`, `active: false`,
`class: "PROJECTED band ⋈ MEASURED sample (joined, never fused)"`, note = *"Band-departure
anomaly is COMPUTED every tick but NOT yet operator-visible: a new model-derived PROJECTED class
ships only after a real step is captured live ... writes zero bytes to the replay digest."* The
lane is wired + honest; with the flag off it states OFF.

## Honest scope (the documented gate-flip step)

The DETERMINISTIC producer gate PASSES and the lane is wired. What remains (task #129) is the
live stateful forecast-band hook + a real step capture: the forecast lane records each tick's
band per series, the next tick's realized sample is compared, and a genuine regime change is
flagged live (the FP defense confirmed on a real noisy series). That capture flips
`phaseCDepartureGatePassed` to true and makes the lane operator-visible. The capacity-crossing
half of cap. C already shipped with A (a config-relative bar through the unchanged
`t.State.Crossed()` path — MEASURED-vs-MEASURED, byte-identical).
