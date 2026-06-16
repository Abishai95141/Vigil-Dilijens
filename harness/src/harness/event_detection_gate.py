"""Event-detection gate (graph-robustness #2, G1 / doc 07 §3.1 + §3.4 / doc 11 §3.5).

Certifies the EVENT-DRIVEN phenomenon detection + cascade recognition over a FROZEN
corpus, offline, no cluster. A discrete event the KG authors as a REQUIRED member of a
phenomenon (OOMKilled → OOM_KILL_CGROUP, CrashLoopBackOff → PROBE_FAILURE_RESTART)
produces a DEGRADED, MEASURED phenomenon finding; joined with a fingerprint trigger
finding it lights up the AUTHORED cascade. These ride OFF the fingerprint digest. This
is a STANDALONE deterministic gate (events are not in the replay bundle): each scenario
folds synthetic-but-real-shaped events through the REAL eventdetect.Findings +
detect.Matcher.Cascades + replay.Digest, with a LABEL ORACLE fixing ground truth
INDEPENDENTLY of the producer. FULLY DETERMINISTIC — exact equalities, no model in loop.

Floors:
  1. DETECTION-FIDELITY  — the produced findings EXACTLY match the oracle (right
                           phenomenon, on the resolved role, degraded). No missing, none
                           extra.
  2. NO-FALSE-UPGRADE==0 — a role-unresolved event (role-less Node, unseen object) or an
                           unauthored reason produces ZERO findings (never assert a
                           phenomenon on an unidentified entity; never the firehose).
  3. CASCADE-RECOGNITION — every authored cascade the oracle expects (leak→OOM,
                           throttle→probe) is recognized, with the AUTHORED `why`
                           verbatim and the right relatedness. No phantom cascade.
  4. DEGRADED-HONEST     — every produced finding is DEGRADED with requiredMet==1 <
                           requiredTotal and the missing members NAMED (degrade-never-
                           fabricate); the satisfied member is a k8s-event member.
  5. DIGEST-INVARIANCE   — digestBefore == digestAfter: producing the event findings +
                           cascades is read-only w.r.t. the fingerprint digest (events
                           ride OFF the digest; join, never fuse).
  6. CHARTER == 0        — no finding/cascade row restates a cause (an event is a
                           co-occurrence; a cascade is an authored edge lit, not a proof).

A substantive VIOLATION fails the gate. Absent any violation, a corpus missing a
required scenario is INSUFFICIENT — never a pass.
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass, field
from pathlib import Path

REQUIRED_SCENARIOS = {
    "oom-detection",
    "leak-to-oom-cascade",
    "throttle-to-probe-cascade",
    "role-unresolved-no-upgrade",
    "unrelated-reason",
    "healthy-negative",
}

# An event is a CO-OCCURRENCE and a cascade is an authored edge lit — never a cause. The
# authored notes/whys are curated to say so without these tokens. A backstop, not a proof.
DENYLIST = [
    "because",
    "caused by",
    "caused the",
    "causes the",
    "due to",
    "root cause",
    "leads to",
    "led to",
    "results in",
    "will cross",
    "will reach",
    "will surpass",
]


@dataclass
class GateReport:
    scenarios: set = field(default_factory=set)
    findings_graded: int = 0
    cascades_recognized: int = 0
    fidelity_violations: list = field(default_factory=list)
    false_upgrades: list = field(default_factory=list)
    cascade_violations: list = field(default_factory=list)
    degraded_violations: list = field(default_factory=list)
    digest_violations: list = field(default_factory=list)
    charter_violations: list = field(default_factory=list)


def _finding_key(f: dict) -> tuple[str, str]:
    return (f.get("Phenomenon", ""), f.get("EntityCEI", ""))


def score(bundles: dict[str, tuple[list[dict], list[dict], dict]]) -> GateReport:
    g = GateReport()
    for name, (findings, cascades, label) in bundles.items():
        scenario = label.get("scenario", name)
        g.scenarios.add(scenario)

        # charter (findings + cascades blob)
        blob = (json.dumps(findings) + json.dumps(cascades)).lower()
        for p in DENYLIST:
            if p in blob:
                g.charter_violations.append(f"{name}: row carried banned phrase {p!r}")

        # digest-invariance
        if label.get("digestBefore") != label.get("digestAfter"):
            g.digest_violations.append(
                f"{name}: fp digest changed (event detection perturbed the digest)"
            )

        # detection-fidelity: produced findings EXACTLY match the oracle
        produced = {_finding_key(f): f for f in findings}
        expected = {
            (e["phenomenon"], e["entityCei"]): e for e in (label.get("expectFindings") or [])
        }
        for key, e in expected.items():
            f = produced.get(key)
            if f is None:
                g.fidelity_violations.append(f"{name}: expected finding {key} NOT produced")
                continue
            if f.get("Quality") != e.get("quality"):
                g.fidelity_violations.append(
                    f"{name}: {key} quality {f.get('Quality')!r} != oracle {e.get('quality')!r}"
                )
            if f.get("RequiredMet") != e.get("requiredMet") or f.get("RequiredTotal") != e.get(
                "requiredTotal"
            ):
                g.fidelity_violations.append(
                    f"{name}: {key} required {f.get('RequiredMet')}/{f.get('RequiredTotal')}"
                    f" != oracle {e.get('requiredMet')}/{e.get('requiredTotal')}"
                )
        # no-false-upgrade: any produced finding NOT in the oracle is a false upgrade
        for key in produced:
            if key not in expected:
                g.false_upgrades.append(f"{name}: produced an unexpected finding {key}")
        g.findings_graded += len(findings)

        # degraded-honest: every produced finding degraded, 1<total, missing members named,
        # satisfied member is a k8s-event member
        for f in findings:
            met, total = f.get("RequiredMet", 0), f.get("RequiredTotal", 0)
            unobs = f.get("Unobservable") or []
            if f.get("Quality") != "degraded" or met < 1 or met >= total:
                g.degraded_violations.append(
                    f"{name}: {_finding_key(f)} not honestly degraded "
                    f"(quality={f.get('Quality')} {met}/{total})"
                )
            if len(unobs) != total - met:
                g.degraded_violations.append(
                    f"{name}: {_finding_key(f)} named {len(unobs)} missing != {total - met}"
                )
            members = f.get("Members") or []
            if not any(str(m.get("Metric", "")).startswith("k8s-event:") for m in members):
                g.degraded_violations.append(
                    f"{name}: {_finding_key(f)} has no k8s-event member (the discrete evidence)"
                )

        # cascade-recognition: every expected cascade present + verbatim why; no phantom
        rec = {(c["Trigger"]["Phenomenon"], c["Downstream"]["Phenomenon"]): c for c in cascades}
        g.cascades_recognized += len(cascades)
        exp_c = {(c["trigger"], c["downstream"]): c for c in (label.get("expectCascades") or [])}
        for key, ce in exp_c.items():
            c = rec.get(key)
            if c is None:
                g.cascade_violations.append(f"{name}: expected cascade {key} NOT recognized")
                continue
            if ce.get("why") and c.get("Why") != ce["why"]:
                g.cascade_violations.append(
                    f"{name}: cascade {key} why {c.get('Why')!r} != authored {ce['why']!r}"
                )
            if ce.get("related") and c.get("Related") != ce["related"]:
                g.cascade_violations.append(
                    f"{name}: cascade {key} related {c.get('Related')!r} != {ce['related']!r}"
                )
        for key in rec:
            if key not in exp_c:
                g.cascade_violations.append(f"{name}: phantom cascade {key} (not in oracle)")

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
    if g.false_upgrades:
        reasons.append("NO-FALSE-UPGRADE > 0: " + "; ".join(g.false_upgrades))
    if g.cascade_violations:
        reasons.append("CASCADE-RECOGNITION broken: " + "; ".join(g.cascade_violations))
    if g.degraded_violations:
        reasons.append("DEGRADED-HONEST broken: " + "; ".join(g.degraded_violations))
    if g.digest_violations:
        reasons.append("DIGEST-INVARIANCE broken: " + "; ".join(g.digest_violations))
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
        "event-detection gate — class: measured_event_driven_detection + authored_cascade",
        f"  scenarios:          {sorted(g.scenarios)}",
        f"  findings graded:    {g.findings_graded}",
        f"  cascades recognized:{g.cascades_recognized}",
        f"  detection-fidelity: {ok(g.fidelity_violations)} (produced == oracle, exact)",
        f"  no-false-upgrade:   {len(g.false_upgrades)} (max 0; unresolved/unauthored = nothing)",
        f"  cascade-recognition:{ok(g.cascade_violations)} (authored why verbatim, no phantom)",
        f"  degraded-honest:    {ok(g.degraded_violations)} (1<total, missing named, event member)",
        f"  digest-invariance:  {ok(g.digest_violations)} (event detection off the fp digest)",
        f"  charter:            {len(g.charter_violations)} violations (max 0)",
    ]
    if v.insufficient:
        lines.append(f"  GATE: INSUFFICIENT — {'; '.join(v.reasons)} (never a pass)")
    elif v.passed:
        lines.append(
            "  GATE: PASSED — event-driven detection + the authored cascades are certified"
            " deterministic; the lane may be surfaced behind --events-enabled (doc 11 §3.5)"
        )
    else:
        lines.append(f"  GATE: FAILED — {'; '.join(v.reasons)}")
    return "\n".join(lines)


def load_rows(path: str | Path) -> list[dict]:
    return [json.loads(line) for line in Path(path).read_text().splitlines() if line.strip()]


def load_label(path: str | Path) -> dict:
    return json.loads(Path(path).read_text())


def main() -> int:
    ap = argparse.ArgumentParser(description="event-detection gate (graph-robustness #2 G1)")
    ap.add_argument("--findings", required=True, nargs="+", help="per-scenario findings JSONL")
    ap.add_argument("--cascades", required=True, nargs="+", help="per-scenario cascades JSONL")
    ap.add_argument("--labels", required=True, nargs="+", help="ground-truth oracle JSON(s)")
    args = ap.parse_args()
    if not (len(args.findings) == len(args.cascades) == len(args.labels)):
        ap.error("--findings, --cascades and --labels must pair up")

    bundles: dict[str, tuple[list[dict], list[dict], dict]] = {}
    for fnd, csc, lbl in zip(args.findings, args.cascades, args.labels, strict=True):
        label = load_label(lbl)
        bundles[label.get("bundle", fnd)] = (load_rows(fnd), load_rows(csc), label)

    g = score(bundles)
    v = gate(g)
    print(render(g, v))
    return 0 if v.passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
