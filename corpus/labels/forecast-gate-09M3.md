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

## Evidence B — representative slow-creep corpus

Workload: `corpus/chaos/leak-slow.yaml` — the class's TARGET scenario (doc 09
§3.8 hours-scale creep, scaled to dev): ~18 min flat hold (context fills),
then one clean ~12 MiB/min ramp to the 0.95×256Mi bar, no restart inside the
context window.

(results appended after the capture run)
