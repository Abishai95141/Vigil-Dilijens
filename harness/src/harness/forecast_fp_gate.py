"""Forecast DECISION-LAYER false-positive gate (the near-miss/recede decoy corpus).

The live forecast backtest (harness.forecast_gate) only ever scores bundles that END IN A
REAL CROSSING, so its false-warning floor is asserted against an EMPTY set — "0 false
warnings" was vacuous. This gate closes the precision blind spot at the layer we own: the
WARNING DECISION code (forecast.Project + the §3.6 guardrails). Each scenario folds the REAL
forecast.Project over a scripted forecast trajectory and freezes its candidate-or-silence
verdict; this gate grades those frozen verdicts against an INDEPENDENT label oracle.

SCOPE — read this before citing the result:
  It certifies that IF the clock forecasts a non-crossing (a point trajectory that plateaus
  or recedes, even with a wide band that touches the bar), OUR decision layer stays SILENT;
  and that a genuine crossing still EMITS. It does NOT certify that TimesFM forecasts
  non-crossings correctly — the model's raw skill at recognising "this will not cross" is a
  separate, model-bearing, on-demand concern (the live clockd backtest, just forecast-gate).
  Never read a pass here as "forecast precision validated" or "TimesFM 0 false warnings".

Floors:
  1. FP-ON-NEARMISS == 0 (CARDINAL) — every recede/plateau/seductive/band-too-wide scenario
     (expectWarning=false) produces NO candidate. The false-warning the live corpus can't make.
  2. SILENCE-REASON exact — a silenced scenario carries the EXACT oracle reason (no-crossing vs
     band-too-wide), so a regression that silences for the wrong reason is caught.
  3. RECALL — every genuine-crossing control (expectWarning=true) still EMITS.
  4. PROJECTED-CLASS — every emitted candidate is class PROJECTED + isProjection=true.
  5. CHARTER == 0 — no candidate carries causal / certainty-of-the-future language.
  6. PARAMS PINNED — every label records the decision-boundary params; a drift from the pinned
     set makes the frozen verdict meaningless, so it fails (not silently re-interpreted).

A substantive VIOLATION fails the gate; a corpus missing a required scenario is INSUFFICIENT.
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass, field
from pathlib import Path

# The six scenarios that MUST be present for the gate to mean anything — the recede/plateau
# defence, the seductive wide-band, the band-too-wide guard, an emit control, and the
# below-direction mirror. A reviewer cannot silently drop one and still PASS.
REQUIRED_SCENARIOS = {
    "recede-above",
    "plateau-below",
    "seductive-wide-upper",
    "band-too-wide",
    "imminent-crossing-above",
    "recede-below",
}

# The decision boundary is only meaningful at these params; the frozen verdict was produced
# under them, so a label that drifts is rejected.
EXPECTED_PARAMS = {"maxBandRatio": 0.5, "horizonSteps": 64, "quantiles": [0.1, 0.5, 0.9]}

DENYLIST = [
    "caused", "causes", "causing", "because of", "due to", "root cause",
    "will cross", "certain to", "guaranteed", "leads to", "results in",
]


@dataclass
class GateReport:
    scenarios: set = field(default_factory=set)
    results_graded: int = 0
    fp_violations: list = field(default_factory=list)
    recall_violations: list = field(default_factory=list)
    silence_violations: list = field(default_factory=list)
    class_violations: list = field(default_factory=list)
    charter_violations: list = field(default_factory=list)
    param_violations: list = field(default_factory=list)
    emit_controls: int = 0


def score(bundles: dict[str, tuple[dict, dict]]) -> GateReport:
    g = GateReport()
    for name, (result, label) in bundles.items():
        scenario = label.get("scenario", name)
        g.scenarios.add(scenario)
        g.results_graded += 1
        expect = bool(label.get("expectWarning"))
        emitted = bool(result.get("emitted"))
        silence = result.get("silence") or ""
        cand = result.get("candidate")

        # params must match the pinned decision boundary
        if label.get("params") != EXPECTED_PARAMS:
            g.param_violations.append(f"{name}: params drift from pinned {EXPECTED_PARAMS}")

        if not expect:
            # CARDINAL: a near-miss/decoy must NOT warn; if silent, the reason must be exact.
            if emitted:
                g.fp_violations.append(f"{name}: {label.get('kind','?')} produced a FALSE warning")
            elif silence != label.get("expectSilence"):
                want = label.get("expectSilence")
                g.silence_violations.append(f"{name}: silenced {silence!r}, oracle wanted {want!r}")
            continue

        # recall: a genuine crossing must emit, PROJECTED, charter-clean.
        if not emitted:
            g.recall_violations.append(f"{name}: genuine crossing, NO warning (recall miss)")
            continue
        g.emit_controls += 1
        if (cand or {}).get("class") != "PROJECTED" or not (cand or {}).get("isProjection"):
            g.class_violations.append(f"{name}: emit not PROJECTED/isProjection")
        blob = json.dumps(cand).lower()
        g.charter_violations += [f"{name}: banned phrase {p!r}" for p in DENYLIST if p in blob]
    return g


@dataclass
class GateVerdict:
    passed: bool
    insufficient: bool
    reasons: list


def gate(g: GateReport) -> GateVerdict:
    reasons: list[str] = []
    if g.fp_violations:
        reasons.append("FP-ON-NEARMISS > 0 (CARDINAL): " + "; ".join(g.fp_violations))
    if g.silence_violations:
        reasons.append("SILENCE-REASON wrong: " + "; ".join(g.silence_violations))
    if g.recall_violations:
        reasons.append("RECALL broken: " + "; ".join(g.recall_violations))
    if g.class_violations:
        reasons.append("PROJECTED-CLASS broken: " + "; ".join(g.class_violations))
    if g.charter_violations:
        reasons.append("charter: " + "; ".join(g.charter_violations))
    if g.param_violations:
        reasons.append("PARAMS drift: " + "; ".join(g.param_violations))
    if reasons:
        return GateVerdict(passed=False, insufficient=False, reasons=reasons)

    missing = REQUIRED_SCENARIOS - g.scenarios
    if missing:
        return GateVerdict(False, True, [f"missing required scenario(s): {sorted(missing)}"])
    # the charter/class checks only bite the emit controls; require at least one.
    if g.emit_controls < 1:
        return GateVerdict(False, True, ["no emit-control scenario — class/charter checks vacuous"])
    return GateVerdict(passed=True, insufficient=False, reasons=[])


def render(g: GateReport, v: GateVerdict) -> str:
    ok = lambda bad: "OK" if not bad else "BROKEN"  # noqa: E731
    lines = [
        "forecast decision-layer FP gate — class: PROJECTED early-warning decision (off-digest)",
        f"  scenarios:       {sorted(g.scenarios)}",
        f"  results graded:  {g.results_graded} (emit controls: {g.emit_controls})",
        f"  fp-on-nearmiss:  {len(g.fp_violations)} (CARDINAL, max 0; recede/plateau never warn)",
        f"  silence-reason:  {ok(g.silence_violations)} (exact no-crossing vs band-too-wide)",
        f"  recall:          {ok(g.recall_violations)} (every genuine crossing emits)",
        f"  projected-class: {ok(g.class_violations)} (every emit is PROJECTED + isProjection)",
        f"  charter:         {len(g.charter_violations)} (max 0)",
        f"  params pinned:   {ok(g.param_violations)} ({EXPECTED_PARAMS})",
    ]
    if v.insufficient:
        lines.append(f"  GATE: INSUFFICIENT — {'; '.join(v.reasons)} (never a pass)")
    elif v.passed:
        lines.append(
            "  GATE: PASSED — the early-warning DECISION layer is certified: IF the clock"
            " forecasts a non-crossing (recede/plateau, even with a wide band touching the"
            " bar) it stays SILENT, and a genuine crossing still emits. SCOPE: certifies OUR"
            " decision code, NOT TimesFM's skill — that is the on-demand live forecast-gate."
        )
    else:
        lines.append(f"  GATE: FAILED — {'; '.join(v.reasons)}")
    return "\n".join(lines)


def load_json(path: str | Path) -> dict:
    return json.loads(Path(path).read_text())


def main() -> int:
    ap = argparse.ArgumentParser(description="forecast decision-layer FP gate (near-miss corpus)")
    ap.add_argument("--results", required=True, nargs="+")
    ap.add_argument("--labels", required=True, nargs="+")
    args = ap.parse_args()
    if len(args.results) != len(args.labels):
        ap.error("--results and --labels must pair up")
    bundles: dict[str, tuple[dict, dict]] = {}
    for rp, lbl in zip(args.results, args.labels, strict=True):
        label = load_json(lbl)
        bundles[label.get("bundle", rp)] = (load_json(rp), label)
    g = score(bundles)
    v = gate(g)
    print(render(g, v))
    return 0 if v.passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
