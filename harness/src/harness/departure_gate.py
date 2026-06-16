"""Departure (band-anomaly) gate (doc 15 cap. C / doc 11 §3.5 / task #75).

Certifies the departure-from-own-forecast-band producer over a FROZEN near-miss/decoy
corpus, offline: a MEASURED sample that left its own PROJECTED forecast band is flagged
(classed PROJECTED), and a noisy-but-stationary series (whose clock band is WIDE) is NOT —
the structural FP defense. Each scenario was folded through the REAL departure.Detect (a
pure function), graded against a LABEL ORACLE.

The flag is model-derived (the band is the clock's forecast, PROJECTED by class), so this
gate certifies the JOIN, not the tick-digest — its determinism is the honest, narrower one
("same recorded band + sample + params ⇒ same flags"); the Go drift guard proves the frozen
corpus reproduces byte-identically.

Floors:
  1. FP-ON-DECOYS == 0 (CARDINAL) — a decoy/healthy scenario (expectDeparture=false) produces
     ZERO departures. A noisy-but-stationary series, a wide-band bump, a near-miss within the
     structural margin, an on-edge sample — none is a departure. The anti-false-warning floor.
  2. RECALL — every true-step scenario (expectDeparture=true) produces a departure.
  3. SIDE-FIDELITY — a firing departure's side matches the oracle (above | below).
  4. PROJECTED-CLASS — every departure is classed PROJECTED (band ⋈ measured), never a
     standalone MEASURED anomaly score (no class laundering, the killed-shortcut guard).
  5. CHARTER == 0 — no causal claim and no "anomaly score" language; the system never asserts
     the departure as a measured fact or a cause.

A substantive VIOLATION fails the gate; a corpus missing a required scenario is INSUFFICIENT.
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass, field
from pathlib import Path

REQUIRED_SCENARIOS = {
    "step-above",
    "step-below",
    "noisy-decoy",
    "wide-band-absorbs",
    "near-miss-margin",
    "healthy-inside",
    "edge-inside",
    "zero-width-band",
}

DENYLIST = [
    "caused", "causes", "causing", "because of", "due to", "root cause",
    "anomaly score", "is an anomaly", "leads to", "results in",
]


@dataclass
class GateReport:
    scenarios: set = field(default_factory=set)
    departures_graded: int = 0
    fp_violations: list = field(default_factory=list)
    recall_violations: list = field(default_factory=list)
    side_violations: list = field(default_factory=list)
    class_violations: list = field(default_factory=list)
    charter_violations: list = field(default_factory=list)


def score(bundles: dict[str, tuple[list[dict], dict]]) -> GateReport:
    g = GateReport()
    for name, (deps, label) in bundles.items():
        scenario = label.get("scenario", name)
        g.scenarios.add(scenario)
        g.departures_graded += len(deps)
        expect = bool(label.get("expectDeparture"))

        # CARDINAL: a non-anomaly (decoy/healthy) must produce ZERO departures
        if not expect and deps:
            kind = label.get("kind", "?")
            g.fp_violations.append(f"{name}: {kind} produced {len(deps)} FALSE departure(s)")
        # recall: a true step must fire
        if expect and not deps:
            g.recall_violations.append(f"{name}: a true step produced NO departure (recall miss)")
        # side-fidelity
        for d in deps:
            if expect and label.get("expectSide") and d.get("side") != label.get("expectSide"):
                g.side_violations.append(
                    f"{name}: side {d.get('side')} != oracle {label.get('expectSide')}"
                )
            # projected-class: never a standalone MEASURED anomaly
            cls = (d.get("class") or "")
            if not cls.startswith("PROJECTED"):
                g.class_violations.append(f"{name}: departure class {cls!r} is not PROJECTED")

        # charter
        blob = json.dumps(deps).lower()
        for p in DENYLIST:
            if p in blob:
                g.charter_violations.append(f"{name}: departure carried banned phrase {p!r}")

    return g


@dataclass
class GateVerdict:
    passed: bool
    insufficient: bool
    reasons: list


def gate(g: GateReport) -> GateVerdict:
    reasons: list[str] = []
    if g.fp_violations:
        reasons.append("FP-ON-DECOYS > 0 (CARDINAL): " + "; ".join(g.fp_violations))
    if g.recall_violations:
        reasons.append("RECALL broken: " + "; ".join(g.recall_violations))
    if g.side_violations:
        reasons.append("SIDE-FIDELITY broken: " + "; ".join(g.side_violations))
    if g.class_violations:
        reasons.append("PROJECTED-CLASS broken: " + "; ".join(g.class_violations))
    if g.charter_violations:
        reasons.append("charter: " + "; ".join(g.charter_violations))
    if reasons:
        return GateVerdict(passed=False, insufficient=False, reasons=reasons)

    missing = REQUIRED_SCENARIOS - g.scenarios
    if missing:
        reason = f"missing required scenario(s): {sorted(missing)}"
        return GateVerdict(passed=False, insufficient=True, reasons=[reason])
    return GateVerdict(passed=True, insufficient=False, reasons=[])


def render(g: GateReport, v: GateVerdict) -> str:
    ok = lambda bad: "OK" if not bad else "BROKEN"  # noqa: E731
    lines = [
        "departure gate — class: projected_band_departure (anomaly, off-digest)",
        f"  scenarios:        {sorted(g.scenarios)}",
        f"  departures graded:{g.departures_graded}",
        f"  fp-on-decoys:     {len(g.fp_violations)} (CARDINAL, max 0; decoys never false-fire)",
        f"  recall:           {ok(g.recall_violations)} (every true step departs)",
        f"  side-fidelity:    {ok(g.side_violations)} (above/below matches the oracle)",
        f"  projected-class:  {ok(g.class_violations)} (never a MEASURED anomaly score)",
        f"  charter:          {len(g.charter_violations)} violations (max 0)",
    ]
    if v.insufficient:
        lines.append(f"  GATE: INSUFFICIENT — {'; '.join(v.reasons)} (never a pass)")
    elif v.passed:
        lines.append(
            "  GATE: PASSED — the band-departure anomaly producer is certified: a measured sample"
            " leaving its own projected band fires (PROJECTED, off-digest), and a noisy/wide-band"
            " series never false-fires (the band is the bar). The lane is gate-pending for the"
            " operator until a real step is captured live (doc 11 §3.5)."
        )
    else:
        lines.append(f"  GATE: FAILED — {'; '.join(v.reasons)}")
    return "\n".join(lines)


def load_rows(path: str | Path) -> list[dict]:
    return [json.loads(line) for line in Path(path).read_text().splitlines() if line.strip()]


def load_label(path: str | Path) -> dict:
    return json.loads(Path(path).read_text())


def main() -> int:
    ap = argparse.ArgumentParser(description="departure gate (doc 15 cap. C)")
    ap.add_argument("--departures", required=True, nargs="+")
    ap.add_argument("--labels", required=True, nargs="+")
    args = ap.parse_args()
    if len(args.departures) != len(args.labels):
        ap.error("--departures and --labels must pair up")
    bundles: dict[str, tuple[list[dict], dict]] = {}
    for dp, lbl in zip(args.departures, args.labels, strict=True):
        label = load_label(lbl)
        bundles[label.get("bundle", dp)] = (load_rows(dp), label)
    g = score(bundles)
    v = gate(g)
    print(render(g, v))
    return 0 if v.passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
