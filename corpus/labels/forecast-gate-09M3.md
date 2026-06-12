# 09 M3 — forecast backtest calibration gate: working-set → OOM class

Evidence log (doc 11 §3.5 / M5). Machinery: `replay -forecast` re-runs the REAL
funnel + Tier-B budget + runner "as of" every recorded tick against live clockd
(TimesFM 2.5, pins per doc 14 A15), tracing raw trajectories;
`harness.forecast_gate` scores them against the realized future from the same
bundle. Gate criteria (v1, with rationale) live in
`harness/src/harness/forecast_gate.py`. The deterministic verdict was
byte-identical (135/135 ticks) in the SAME runs — the forecast pass perturbs
nothing.

## Evidence A — sawtooth corpus (2026-06-13, /tmp/vigil-p2/store, 135 ticks)

Workload: `corpus/chaos/leak-oom.yaml` — fast leak cycling through FIVE
OOM-kill episodes (~6 min each). The forecast context therefore contains prior
ramp→kill→reset cycles: a SAWTOOTH.

```
scored forecasts: 716 (skipped short-future: 79)
band coverage:    0.845 over 26,323 realized points (nominal 0.8)  ✓ inside [0.65, 0.98]
crossings:        46 actual · 1 warned · 45 missed {band-too-wide: 14, no-crossing: 31}
in-band:          1/1 actual crossings inside [earliest, latest]
false warnings:   0/1 (full-horizon observations)                  ✓
ttc error:        median +23 steps · median |frac| 3.286           ✗
GATE: FAILED — crossing recall 0.022 < 0.7; ttc |frac| 3.286 > 0.35
```

**Reading (trace-level):** the model's point trajectories on ramp ticks DESCEND
(e.g. point 87.9→58.9 MB while reality climbed to the 95.6 MB bar): given a
sawtooth history, the shape-driven zero-shot clock projects the sawtooth's
continuation — the next RESET — not the administrative bar crossing. Its
uncertainty stays honest (coverage 0.845) and it never cries wolf (0 false
warnings); it simply does not anticipate a crossing whose precedent in-context
is "climb then vanish".

**This is the doc-anticipated failure**, not a surprise: doc 09 §3.4 — "feeding
it the past spike only pollutes the forecast" — and the designed remedy is
Phase 3 decomposition/SPLICE POINTS (context boundaries at known events; a
container restart is exactly such a boundary, already known to the identity
layer). Recorded as the driving requirement for 09 M5.

**Gate consequence:** the class does NOT become operator-visible on sawtooth
evidence; per the gate rule the early-warning lane stays dark unless the
representative corpus (below) passes.

## Evidence B1 — slow-creep corpus (leak-slow.yaml, 124 ticks) — and a GATE LESSON

One clean ramp, no reset in context. Result: band coverage 0.737 ✓, every
silence correct, zero candidates — and **zero realized crossings to score**:
the scraped working-set never touched the 0.95×limit bar, because the
above-bar window before the kill (~5 s at this ramp rate) fell BETWEEN 15 s
scrapes. The original criteria returned a hollow PASS on coverage + silences
alone.

**Gate hardened in response:** `MIN_CROSSINGS = 3` — a crossing-warning class
ships on CROSSING evidence, never on its absence; B1 now reads INSUFFICIENT
(regression test pins this). Corpus lesson: the crossing must be
scrape-visible — `corpus/chaos/leak-plateau.yaml` ramps a tmpfs-backed leak to
a HELD plateau above the bar (no kill race; minutes of above-bar evidence).

## Evidence C+D+E — plateau corpora (leak-plateau.yaml ×3 cycles) — THE GATE RUN

Three clean-context creep events (each its own pod/stream; ~12 MiB/min ramps
to a HELD plateau above the 0.95×512Mi bar), scored together with every other
working-set target on the cluster (552 ticks, 3,870 scored forecasts;
byte-identity held in every replay run, 135+124+146+259+159 ticks total
across the M3 campaign).

```
band coverage:    0.755 over 213,978 realized points (nominal 0.8)   ✓
events:           3 class-eligible crossings · 3 warned ≥8 steps lead
                  (event recall 1.00; observed leads 20–22 steps ≈ 5 min) ✓
hover exclusion:  1 at-bar hover crossing (currencyservice, the deliberately
                  near-limit service) excluded WITH reason — a hover re-crossing
                  has no approach to anticipate; the at-threshold detection
                  ladder owns it continuously                          ✓
in-band:          15/16 actual crossings inside [earliest, latest]    ✓ (≥0.8)
false warnings:   0/16 full-horizon observations                      ✓
ttc point error:  median +8 steps · median |frac| 0.477 (REPORTED diagnostic —
                  the band is the promise and the band held)
GATE: PASSED — the class may become operator-visible (doc 11 §3.5)
```

**Criteria refinement trail (every change evidence-driven, in commit order):**
1. A (sawtooth) → class FAILS on cycling-reset context; remedy recorded = 09 M5
   splice points. Standing stated limitation of the v1 class.
2. B1 → MIN_CROSSINGS: crossing evidence is mandatory (a hollow pass on
   coverage+silences was possible when the above-bar window fell between scrapes).
3. C → event-level recall with MIN_LEAD (per-tick recall punishes the honesty
   that produces zero false warnings); MIN_CROSSING_EVENTS=3.
4. C+D → IN_BAND_MIN gates the band (doc 11 §3.5 "point and band"; the band is
   what the operator is promised); point error reported, ungated.
5. C+D+E → event class-eligibility via the at-threshold band at a 32-step
   lookback: hover crossings are detection's jurisdiction, excluded with reason.

## 09 M4 — live verification (during capture D, lane enabled as engineering telemetry)

The live warm path against the kind cluster + the real TimesFM clockd produced
an operator-shaped warning on a REAL pod during its ramp, captured from
/api/warnings (~5.2 min lead): PROJECTED class + is_projection; config bar
510.0MB; CLOSED band 21:22:07–21:27:22Z; confidence moderate; AUTHORED
precursor reference PHEN_OOM_KILL_CGROUP from the real KG; 40 reasoned
silences; clock ready; topology predictive mark (violet diamond) = 1;
timeline projected span = 1. At-risk was honestly EMPTY: the KG authors no
downstream relations FROM the OOM phenomenon (it is the terminal event of the
leak relation) — stated, not fabricated. The formal operator-enable decision
is tied to THIS gate verdict; deployments ship with forecast.enabled=false
until governance flips it per class.
