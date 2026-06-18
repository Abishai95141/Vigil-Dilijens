# Regime-shift contamination flag — gate evidence (doc 09 M5 companion)

**Status: PASS — `just regime-shift-gate` (branch v4, 2026-06-18).** Deterministic
Go gate; graph hash UNCHANGED (pure obsd code, no ontology/governance impact).

## What it is

The honest answer to the forecasting blind spot surfaced in the real-world readiness
research: decomposition (doc 09 M5) only removes a footprint at a DECLARED context
window or an auto-detected gauge RESET (a DROP). A config change, deploy, or workload
migration that **raises** the baseline toward the bar leaves no reset and — if
undeclared — no splice, so both regimes enter the clock's input. The worst case: when
the higher baseline is recent, the projection is dominated by the **stale low** history
and reads "flat, no crossing" — falsely reassuring exactly when the level just lurched
toward the bar.

`forecast.DetectRegimeShift` (`obsd/internal/forecast/regimeshift.go`) detects an
**undeclared upward level shift that plateaus** in the post-decomposition input and:
- **SILENCES** it (`SilenceRegimeShift`) when the new regime has < `min_context` points
  (the projection would reflect the stale baseline) — wired in `runner.go` RunCycle;
- otherwise **FLAGS** the candidate (`Candidate.RegimeShift`, surfaced as
  `WarningCard.Contamination` on `/api/warnings`) — a MEASURED caveat that the band may
  be inflated; the remedy is to declare a context window.

## The CARDINAL rule (the one unacceptable error)

**A genuine leak/ramp is NEVER flagged as contamination.** An upward ramp IS the
early-warning signal the forecaster exists to find; auto-removing it (or discrediting
it) would blind the warning. So the detector:
- considers only UPWARD jumps (a drop is a reset, decompose's job);
- requires a **relative-rise floor** (`post ≥ pre × (1 + regime_shift_fraction)`) so a
  small wobble on a flat series is ignored;
- requires **both** regimes to be roughly flat (`segDrift ≤ regime_shift_plateau ×
  jump`) — a STEP that PLATEAUS, never an ongoing or stabilising ramp. THIS is the
  leak-protection guard: a sustained climb (any slope) always fails it.

It NEVER trims the model input (charter: the operator's declared context window is the
only thing that cleans history — borrowed normativity). No learned thresholds; all
constants AUTHORED in `params/defaults.dev.yaml` (`regime_shift_fraction: 0.35`,
`regime_shift_min_segment: 8`, `regime_shift_plateau: 0.35`). Non-gating (detection
untouched). Deterministic / replay-stable.

## Test battery (`go test -race ./obsd/internal/forecast/ -run RegimeShift`)

- `TestDetectRegimeShift` — 15 cases: pure/steep/gentle ramps and the real
  `leak-saw-slow` ramp shape (3.8→191.7) → **never flagged**; flat-step-flat (recent →
  severe; old → flagged-not-severe; with noise) → flagged UP; flat, noisy-flat, a
  step-DOWN, a single spike, a sub-floor 8% step, and a too-short series → not flagged.
- `TestDetectRegimeShiftLeakCorpusShapes` — the **cardinal battery**: slow/fast/noisy/
  accelerating creeps, a post-reset ramp, creep-then-plateau, two-stage creep → **zero
  false positives**.
- `TestDetectRegimeShiftDisabled` / `…Deterministic` — opt-out honoured; identical
  verdict on repeat.
- `TestRunCycleRegimeShift` — end-to-end through the real `RunCycle`: a recent
  undeclared step under the bar → `SilenceRegimeShift` (no candidate); a leak ramp →
  exactly one candidate, **no** regime-shift silence and **no** contamination flag
  (cardinal rule holds in the production path).

## Honest live status

The detector is computed **on the input series before the clock call** — it does not
depend on TimesFM output at all. The pure function and the real `RunCycle` path are
certified deterministically (gate PASSED, full `-race ./...` green, `just lint` OK). A
live clockd capture would exercise no additional code for an input-only flag, so it is
intentionally **not** part of this gate (unlike decomposition itself, whose forecast
QUALITY required a live A/B — see `decomposition-09M5.md`). Forecasting is gated OFF by
default (`forecast.enabled: false`); when an operator enables the warm path, the flag
is on (`regime_shift_flag: true`) within it.

## Known limitation (stated, not hidden)

The detector deliberately stays silent on a **step-then-resumed-ramp** (a baseline that
jumped then kept climbing) to protect the leak signal — that hybrid is not flagged.
Operator-declared context windows remain the authoritative remedy for any undeclared
event. The flag closes the most dangerous case (a recent step that plateaus, which the
naive forecast reads as reassuring) without ever risking a false positive on a real leak.
