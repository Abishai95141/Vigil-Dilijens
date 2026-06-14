"""Incident-memory backtest gate (v3 T-B / doc 03 + 14 §1.4 / doc 11 §3.5).

Certifies the durable cross-run incident memory over a FROZEN corpus, offline, no
cluster — the recurrence climb the live kind cluster could not produce (its OOM
signals are continuous / unreported). FULLY DETERMINISTIC: there is no model in the
loop, so the gate asserts EXACT equalities.

Floors:
  1. KEY-PURITY      — every incident key is reproducible as sha256(phenomenon | role |
                       bucket); a learned/opaque key fails. (the charter's
                       deterministic-grouping guarantee, recomputed independently here)
  2. GROUPING        — a recurring condition reduces to ONE incident with recurrence N
                       (not N incidents, not one continuous span).
  3. CONTINUOUS CTRL — a continuous (no-gap) condition stays recurrence 1 (no per-tick
                       inflation) — the property the live 12-min span also showed.
  4. RESTART-INVAR.  — the restart bundle's final incidents are IDENTICAL to the
                       recurring bundle's (an incident survives a process restart).
  5. SEPARATION      — distinct phenomena/roles never collapse into one incident.
  6. CHARTER         — no incident field restates a cause or a projection.

A substantive VIOLATION fails the gate. Absent any violation, a corpus missing a
required scenario is INSUFFICIENT — never a pass.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from dataclasses import dataclass, field
from pathlib import Path

KEY_SEP = "\x1f"
REQUIRED_SCENARIOS = {"recurring", "restart", "continuous", "separation"}
# A backstop denylist for an incident payload's register (honest: a backstop, not a
# proof — incidents carry only phenomenon ids + counts + timestamps).
DENYLIST = ["because", "caused by", "root cause", "will cross", "will reach", "due to"]


def incident_key(phenomenon: str, role: str, bucket: str) -> str:
    return hashlib.sha256((phenomenon + KEY_SEP + role + KEY_SEP + bucket).encode()).hexdigest()


def final_incidents(rows: list[dict]) -> list[dict]:
    return rows[-1]["incidents"] if rows else []


@dataclass
class GateReport:
    scenarios: set = field(default_factory=set)
    key_purity_violations: list = field(default_factory=list)
    grouping_violations: list = field(default_factory=list)
    continuous_inflation: list = field(default_factory=list)
    restart_invariance_broken: list = field(default_factory=list)
    separation_broken: list = field(default_factory=list)
    charter_violations: list = field(default_factory=list)


def score(bundles: dict[str, tuple[list[dict], dict]]) -> GateReport:
    g = GateReport()
    finals: dict[str, list[dict]] = {}

    for name, (rows, label) in bundles.items():
        scenario = label.get("scenario", name)
        g.scenarios.add(scenario)
        inc = final_incidents(rows)
        finals[scenario] = inc

        # key-purity (every bundle)
        for i in inc:
            if i["key"] != incident_key(i["phenomenon"], i["roleCei"], i["windowBucket"]):
                g.key_purity_violations.append(
                    f"{name}:{i['phenomenon']} key not reproducible from its inputs"
                )

        # charter (every bundle)
        blob = json.dumps(inc).lower()
        for p in DENYLIST:
            if p in blob:
                g.charter_violations.append(f"{name}: incident payload carried banned phrase {p!r}")

        # grouping / recurrence vs label
        exp_distinct = label.get("expectDistinct")
        exp_rec = label.get("expectRecurrence")
        if exp_distinct is not None and len(inc) != exp_distinct:
            g.grouping_violations.append(f"{name}: distinct {len(inc)} != expected {exp_distinct}")
        if exp_rec is not None and len(inc) == 1 and inc and inc[0]["recurrenceCount"] != exp_rec:
            g.grouping_violations.append(
                f"{name}: recurrence {inc[0]['recurrenceCount']} != expected {exp_rec}"
            )

    # continuous-span control
    cont = finals.get("continuous")
    if cont is not None:
        if len(cont) != 1 or cont[0]["recurrenceCount"] != 1:
            g.continuous_inflation.append(
                f"continuous span inflated: distinct {len(cont)}, recurrence "
                f"{cont[0]['recurrenceCount'] if cont else 'n/a'} (want 1/1)"
            )

    # restart-invariance: restart final == recurring final
    if "restart" in finals and "recurring" in finals:
        a = json.dumps(finals["recurring"], sort_keys=True)
        b = json.dumps(finals["restart"], sort_keys=True)
        if a != b:
            g.restart_invariance_broken.append(
                "restart final incidents differ from the recurring bundle"
            )

    # separation: the separation scenario must not collapse
    sep = finals.get("separation")
    if sep is not None:
        exp = next(
            (
                lbl.get("expectDistinct")
                for _, (_, lbl) in bundles.items()
                if lbl.get("scenario") == "separation"
            ),
            None,
        )
        if exp is not None and len(sep) != exp:
            g.separation_broken.append(f"separation collapsed: {len(sep)} distinct, want {exp}")

    return g


@dataclass
class GateVerdict:
    passed: bool
    insufficient: bool
    reasons: list


def gate(g: GateReport) -> GateVerdict:
    reasons: list[str] = []
    if g.key_purity_violations:
        reasons.append(
            "key-purity broken (a key is not reproducible from its inputs): "
            + "; ".join(g.key_purity_violations)
        )
    if g.grouping_violations:
        reasons.append("grouping/recurrence wrong: " + "; ".join(g.grouping_violations))
    if g.continuous_inflation:
        reasons.append("; ".join(g.continuous_inflation))
    if g.restart_invariance_broken:
        reasons.append("restart-invariance broken: " + "; ".join(g.restart_invariance_broken))
    if g.separation_broken:
        reasons.append("separation broken: " + "; ".join(g.separation_broken))
    if g.charter_violations:
        reasons.append("charter: " + "; ".join(g.charter_violations))
    if reasons:
        return GateVerdict(passed=False, insufficient=False, reasons=reasons)

    missing = REQUIRED_SCENARIOS - g.scenarios
    if missing:
        return GateVerdict(
            passed=False,
            insufficient=True,
            reasons=[f"missing required scenario(s): {sorted(missing)}"],
        )
    return GateVerdict(passed=True, insufficient=False, reasons=[])


def render(g: GateReport, v: GateVerdict) -> str:
    ok = lambda bad: "OK" if not bad else "BROKEN"  # noqa: E731
    lines = [
        "incident-memory backtest — class: measured_cross_run_incident",
        f"  scenarios:        {sorted(g.scenarios)}",
        f"  key-purity:       {ok(g.key_purity_violations)} (key from phenomenon|role|bucket)",
        f"  grouping:         {ok(g.grouping_violations)} (recurring N->1 incident, recurrence N)",
        f"  continuous ctrl:  {ok(g.continuous_inflation)} (no per-tick inflation)",
        f"  restart-invar.:   {ok(g.restart_invariance_broken)} (incident survives restart)",
        f"  separation:       {ok(g.separation_broken)} (distinct phenomena/roles never collapse)",
        f"  charter:          {len(g.charter_violations)} violations (max 0)",
    ]
    if v.insufficient:
        lines.append(f"  GATE: INSUFFICIENT — {'; '.join(v.reasons)} (never a pass)")
    elif v.passed:
        lines.append(
            "  GATE: PASSED — the incident memory's recurrence semantics are certified"
            " (deterministic); it may be surfaced behind this gate (doc 11 §3.5)"
        )
    else:
        lines.append(f"  GATE: FAILED — {'; '.join(v.reasons)}")
    return "\n".join(lines)


def load_rows(path: str | Path) -> list[dict]:
    return [json.loads(line) for line in Path(path).read_text().splitlines() if line.strip()]


def load_label(path: str | Path) -> dict:
    return json.loads(Path(path).read_text())


def main() -> int:
    ap = argparse.ArgumentParser(description="incident-memory backtest gate (v3 T-B)")
    ap.add_argument(
        "--events", required=True, nargs="+", help="per-tick JSONL file(s) from replay -incidents"
    )
    ap.add_argument(
        "--labels",
        required=True,
        nargs="+",
        help="ground-truth label JSON(s), paired with --events",
    )
    args = ap.parse_args()
    if len(args.events) != len(args.labels):
        ap.error("--events and --labels must pair up")

    bundles: dict[str, tuple[list[dict], dict]] = {}
    for ev, lbl in zip(args.events, args.labels, strict=True):
        label = load_label(lbl)
        bundles[label.get("bundle", ev)] = (load_rows(ev), label)

    g = score(bundles)
    v = gate(g)
    print(render(g, v))
    return 0 if v.passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
