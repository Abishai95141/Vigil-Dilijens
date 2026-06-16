"""App-SLO gate (doc 15 cap. A / doc 04 §3.4 / doc 11 §3.5).

Certifies the application-signal lane over a FROZEN corpus, offline, across the three
chain phenomena Capability A ingests — each an app metric crossing its CUSTOMER-DECLARED
SLO producing a MEASURED finding, with an UNDECLARED SLO never fabricating a bar:

  L4 queue     PHEN_APP_QUEUE_SATURATION  — queue depth gauge vs slo.queue.max_depth
  L6 freshness PHEN_APP_DATA_STALENESS — data AGE (evalNow-value) vs slo.freshness.max_age
  L1 load      PHEN_APP_LOAD_SURGE        — request RATE (Δcounter/Δt) vs slo.requests.max_rate

Each scenario was folded through the REAL path — binding.Compile (SLO resolution from
declared config) -> observe.Materialize (the metric laddered vs the declared bar, with the
counter→rate and age-from-timestamp derivations) -> detect.Matcher — with a LABEL ORACLE
fixed by construction (fire iff declared AND crossed). FULLY DETERMINISTIC; the Go drift
guard proves the frozen corpus reproduces.

Floors:
  1. DETECTION-FIDELITY  — for each scenario, a finding for its phenomenon appears iff the
                           oracle's expectFire is true; full quality; on the app pod.
  2. NO-FABRICATION == 0 — an undeclared-SLO scenario produces ZERO findings: a crossing
                           metric with no declared bar is NEVER a finding (the charter ban
                           on learned/default capacity, proven through the real binding).
  3. BORROWED-BAR        — every firing finding's bar is CONFIG-sourced (BarFlagged false):
                           the customer's own SLO, never a flagged default.
  4. NO-CROSS-TALK == 0  — a scenario exercising one phenomenon must NOT light up another
                           app phenomenon: the three app signals are independent (a stale
                           timestamp is not a deep queue is not a load surge).
  5. CHARTER == 0        — no finding row restates a cause.

A substantive VIOLATION fails the gate. Absent any violation, a corpus missing a
required scenario is INSUFFICIENT — never a pass.
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass, field
from pathlib import Path

REQUIRED_SCENARIOS = {
    # L4 queue (the keystone)
    "over-slo",
    "under-slo",
    "undeclared-high-queue",
    "healthy-no-stream",
    # L6 freshness (the differentiator)
    "freshness-stale",
    "freshness-fresh",
    "freshness-undeclared",
    # L1 load (the trigger)
    "load-over",
    "load-under",
    "load-undeclared",
}
# Every application-level phenomenon the lane authors (doc 15 cap. A). A finding whose
# Phenomenon is in this set is an app finding the floors apply to.
APP_PHENS = {
    "PHEN_APP_QUEUE_SATURATION",
    "PHEN_APP_DATA_STALENESS",
    "PHEN_APP_LOAD_SURGE",
}

DENYLIST = [
    "because",
    "caused by",
    "caused the",
    "due to",
    "root cause",
    "leads to",
    "led to",
    "results in",
]


@dataclass
class GateReport:
    scenarios: set = field(default_factory=set)
    phenomena_fired: set = field(default_factory=set)
    findings_graded: int = 0
    fidelity_violations: list = field(default_factory=list)
    fabrications: list = field(default_factory=list)
    bar_violations: list = field(default_factory=list)
    crosstalk_violations: list = field(default_factory=list)
    charter_violations: list = field(default_factory=list)


def score(bundles: dict[str, tuple[list[dict], dict]]) -> GateReport:
    g = GateReport()
    for name, (findings, label) in bundles.items():
        scenario = label.get("scenario", name)
        g.scenarios.add(scenario)
        g.findings_graded += len(findings)

        # The phenomenon this scenario exercises (default to the queue keystone for
        # back-compat with any unlabelled bundle).
        phen = label.get("phenomenon", "PHEN_APP_QUEUE_SATURATION")
        own = [f for f in findings if f.get("Phenomenon") == phen]
        fired = len(own) > 0
        expect = bool(label.get("expectFire"))
        for f in own:
            g.phenomena_fired.add(f.get("Phenomenon"))

        # detection-fidelity: the scenario's phenomenon fired iff expected
        if fired != expect:
            g.fidelity_violations.append(
                f"{name}: {phen} fired={fired} != oracle expectFire={expect}"
            )
        # quality must be full when firing (a declared crossing is unambiguous)
        for f in own:
            if f.get("Quality") != "full":
                g.fidelity_violations.append(f"{name}: quality {f.get('Quality')} != full")

        # no-cross-talk: NO OTHER app phenomenon may fire on this scenario — the three
        # app signals are independent (a stale timestamp must not read as a deep queue).
        for f in findings:
            ph = f.get("Phenomenon")
            if ph in APP_PHENS and ph != phen:
                g.crosstalk_violations.append(
                    f"{name}: exercises {phen} but {ph} also fired (cross-talk)"
                )

        # no-fabrication: an UNDECLARED SLO must never produce ANY app finding
        if not label.get("sloDeclared"):
            for f in findings:
                fp = f.get("Phenomenon")
                if fp in APP_PHENS:
                    g.fabrications.append(
                        f"{name}: produced {fp} with NO declared SLO (fabricated a bar)"
                    )

        # borrowed-bar provenance: a firing app finding's member bar is config-sourced
        for f in findings:
            fp = f.get("Phenomenon")
            if fp not in APP_PHENS:
                continue
            for m in f.get("Members") or []:
                if m.get("BarFlagged"):
                    g.bar_violations.append(
                        f"{name}: {fp} bar is a flagged default, not the customer SLO"
                    )

        # charter
        blob = json.dumps(findings).lower()
        for p in DENYLIST:
            if p in blob:
                g.charter_violations.append(f"{name}: finding carried banned phrase {p!r}")

    return g


@dataclass
class GateVerdict:
    passed: bool
    insufficient: bool
    reasons: list


def gate(g: GateReport) -> GateVerdict:
    reasons: list[str] = []
    if g.fidelity_violations:
        reasons.append("DETECTION-FIDELITY broken: " + "; ".join(g.fidelity_violations))
    if g.fabrications:
        reasons.append("NO-FABRICATION > 0: " + "; ".join(g.fabrications))
    if g.bar_violations:
        reasons.append("BORROWED-BAR broken: " + "; ".join(g.bar_violations))
    if g.crosstalk_violations:
        reasons.append("NO-CROSS-TALK > 0: " + "; ".join(g.crosstalk_violations))
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
        "app-slo gate — class: measured_app_signal_vs_declared_slo (L4 queue, L6 fresh, L1 load)",
        f"  scenarios:        {sorted(g.scenarios)}",
        f"  phenomena fired:  {sorted(g.phenomena_fired)}",
        f"  findings graded:  {g.findings_graded}",
        f"  detection-fidelity:{ok(g.fidelity_violations)} (fire iff declared+crossed)",
        f"  no-fabrication:   {len(g.fabrications)} (max 0; undeclared SLO never fires)",
        f"  borrowed-bar:     {ok(g.bar_violations)} (firing bar is the customer SLO)",
        f"  no-cross-talk:    {len(g.crosstalk_violations)} (max 0; app phenomena are independent)",
        f"  charter:          {len(g.charter_violations)} violations (max 0)",
    ]
    if v.insufficient:
        lines.append(f"  GATE: INSUFFICIENT — {'; '.join(v.reasons)} (never a pass)")
    elif v.passed:
        lines.append(
            "  GATE: PASSED — the application-SLO detection lane is certified deterministic across"
            " L4 queue + L6 freshness + L1 load (borrowed normativity, no fabricated bar, no"
            " cross-talk); the app lane may be surfaced behind --app-metrics-enabled (doc 11 §3.5)"
        )
    else:
        lines.append(f"  GATE: FAILED — {'; '.join(v.reasons)}")
    return "\n".join(lines)


def load_rows(path: str | Path) -> list[dict]:
    return [json.loads(line) for line in Path(path).read_text().splitlines() if line.strip()]


def load_label(path: str | Path) -> dict:
    return json.loads(Path(path).read_text())


def main() -> int:
    ap = argparse.ArgumentParser(description="app-slo gate (doc 15 cap. A)")
    ap.add_argument("--findings", required=True, nargs="+", help="per-scenario findings JSONL")
    ap.add_argument("--labels", required=True, nargs="+", help="ground-truth oracle JSON(s)")
    args = ap.parse_args()
    if len(args.findings) != len(args.labels):
        ap.error("--findings and --labels must pair up")

    bundles: dict[str, tuple[list[dict], dict]] = {}
    for fnd, lbl in zip(args.findings, args.labels, strict=True):
        label = load_label(lbl)
        bundles[label.get("bundle", fnd)] = (load_rows(fnd), label)

    g = score(bundles)
    v = gate(g)
    print(render(g, v))
    return 0 if v.passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
