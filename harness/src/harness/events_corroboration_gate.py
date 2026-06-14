"""Events-corroboration gate (v3 T-C / doc 03 + doc 07 §3.1 / doc 11 §3.5).

Certifies the discrete-event JOIN over a FROZEN corpus, offline, no cluster. k8s
events are not in the replay bundle and never enter the digest, so this is a
STANDALONE deterministic JOIN gate (not a bundle-replay pass): each scenario folds a
synthetic-but-real-shaped set of events through the REAL events.ResolveEventRole +
events.Corroborate + replay.Digest, with a LABEL ORACLE fixing each event's
ground-truth role identity INDEPENDENTLY of the emitter. FULLY DETERMINISTIC — exact
equalities, no model in the loop.

Floors:
  1. JOIN-FIDELITY == 1.0  — every event resolves to its ORACLE role, and corroborates
                             iff the oracle says so. The identity-mismatch scenario
                             (event on role A, gauge on role B) MUST NOT corroborate;
                             a single false join fails the gate (silent identity
                             mis-join is the cardinal sin, doc 03).
  2. NO-PHANTOM == 0       — no corroboration where the oracle says none should exist
                             (an unrelated event must not manufacture corroboration).
  3. STANDALONE-VIS >= 1   — a not-corroborated event (CrashLoopBackOff, a node OOM, an
                             OOMKilled with no gauge on its role) is STILL surfaced as a
                             visible MEASURED finding, never dropped, never upgraded to a
                             match it lacks members for (the blind-spot-closing property).
  4. DIGEST-INVARIANCE     — digestBefore == digestAfter per scenario: the join is
                             read-only w.r.t. the deterministic findings (join, never
                             fuse; events ride OFF the digest).
  5. CHARTER == 0          — no event row restates a cause (an event is a co-occurrence).

A substantive VIOLATION fails the gate. Absent any violation, a corpus missing a
required scenario is INSUFFICIENT — never a pass.
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass, field
from pathlib import Path

REQUIRED_SCENARIOS = {
    "corroborated-oom",
    "multi-role",
    "identity-mismatch",
    "standalone-crashloop",
    "healthy-negative",
}

# A strict register denylist — an event is a CO-OCCURRENCE, never a cause; the
# authored "why" is curated to say so without these tokens (it uses "co-occurrence",
# "corroborates", "does not prove"). A backstop, not a proof.
DENYLIST = [
    "because",
    "caused by",
    "caused the",
    "causes the",
    "causing",
    "due to",
    "root cause",
    "the cause",
    "leads to",
    "led to",
    "results in",
    "will cross",
    "will reach",
]


@dataclass
class GateReport:
    scenarios: set = field(default_factory=set)
    total_events: int = 0
    join_violations: list = field(default_factory=list)
    phantom_violations: list = field(default_factory=list)
    visibility_violations: list = field(default_factory=list)
    standalone_visible: int = 0
    count_violations: list = field(default_factory=list)
    digest_violations: list = field(default_factory=list)
    charter_violations: list = field(default_factory=list)

    def join_fidelity(self) -> float:
        if self.total_events == 0:
            return 1.0
        bad = len(self.join_violations)
        return max(0.0, (self.total_events - bad) / self.total_events)


def score(bundles: dict[str, tuple[list[dict], dict]]) -> GateReport:
    g = GateReport()
    for name, (rows, label) in bundles.items():
        scenario = label.get("scenario", name)
        g.scenarios.add(scenario)
        oracle = {e["entityCei"]: e for e in (label.get("events") or [])}

        # digest-invariance (per scenario)
        if label.get("digestBefore") != label.get("digestAfter"):
            g.digest_violations.append(
                f"{name}: digest changed across the join (events perturbed the digest)"
            )

        # charter (the whole row blob)
        blob = json.dumps(rows).lower()
        for p in DENYLIST:
            if p in blob:
                g.charter_violations.append(f"{name}: event row carried banned phrase {p!r}")

        seen = set()
        for r in rows:
            ev = r["event"]
            ent = ev["entityCei"]
            seen.add(ent)
            g.total_events += 1
            o = oracle.get(ent)
            if o is None:
                g.join_violations.append(f"{name}: row {ent} has no oracle entry (corpus desync)")
                continue
            # resolution fidelity vs the oracle role identity
            if o["oracleResolved"]:
                if ev["roleUnresolved"] or ev["roleCei"] != o["oracleRole"]:
                    g.join_violations.append(
                        f"{name}: {ent} resolved to {ev['roleCei']!r} "
                        f"(unresolved={ev['roleUnresolved']}) != oracle {o['oracleRole']!r}"
                    )
            elif not ev["roleUnresolved"]:
                g.join_violations.append(
                    f"{name}: {ent} should be role-unresolved but resolved to a role"
                )
            # join correctness vs the oracle expectation
            if bool(r["gaugeRoleMatch"]) != bool(o["shouldCorroborate"]):
                g.join_violations.append(
                    f"{name}: {ent} corroborated={r['gaugeRoleMatch']} "
                    f"!= oracle should={o['shouldCorroborate']}"
                )
            # phantom corroboration: joined where the oracle says it must not
            if r["gaugeRoleMatch"] and not o["shouldCorroborate"]:
                g.phantom_violations.append(
                    f"{name}: {ent} manufactured a corroboration the oracle forbids"
                )
            # standalone visibility: a not-corroborated event still surfaced
            if not r["gaugeRoleMatch"]:
                g.standalone_visible += 1

        # visibility: every oracle event must appear as a row (never silently dropped)
        for ent in oracle:
            if ent not in seen:
                g.visibility_violations.append(f"{name}: event {ent} was dropped (not surfaced)")

        # expectation counts (grouping correctness vs the label)
        corro = sum(1 for r in rows if r["gaugeRoleMatch"])
        unres = sum(1 for r in rows if r["event"]["roleUnresolved"])
        stand = sum(1 for r in rows if not r["gaugeRoleMatch"] and not r["event"]["roleUnresolved"])
        if corro != label.get("expectCorroborated", corro):
            g.count_violations.append(
                f"{name}: corroborated {corro} != expected {label['expectCorroborated']}"
            )
        if stand != label.get("expectStandalone", stand):
            g.count_violations.append(
                f"{name}: standalone {stand} != expected {label['expectStandalone']}"
            )
        if unres != label.get("expectUnresolved", unres):
            g.count_violations.append(
                f"{name}: unresolved {unres} != expected {label['expectUnresolved']}"
            )

    return g


@dataclass
class GateVerdict:
    passed: bool
    insufficient: bool
    reasons: list


def gate(g: GateReport) -> GateVerdict:
    reasons: list[str] = []
    if g.join_fidelity() < 1.0:
        reasons.append(
            f"JOIN-FIDELITY {g.join_fidelity():.3f} < 1.0: " + "; ".join(g.join_violations)
        )
    if g.phantom_violations:
        reasons.append("phantom corroboration: " + "; ".join(g.phantom_violations))
    if g.visibility_violations:
        reasons.append("visibility broken (event dropped): " + "; ".join(g.visibility_violations))
    if g.standalone_visible < 1:
        reasons.append(
            "STANDALONE-VISIBILITY 0: no not-corroborated event surfaced (blind spot not closed)"
        )
    if g.count_violations:
        reasons.append("expectation counts wrong: " + "; ".join(g.count_violations))
    if g.digest_violations:
        reasons.append("digest-invariance broken: " + "; ".join(g.digest_violations))
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
        "events-corroboration gate — class: measured_discrete_event_join",
        f"  scenarios:        {sorted(g.scenarios)}",
        f"  events graded:    {g.total_events}",
        f"  JOIN-FIDELITY:    {g.join_fidelity():.3f} (want 1.000; resolved==oracle)",
        f"  no-phantom:       {ok(g.phantom_violations)} (no corroboration the oracle forbids)",
        f"  standalone-vis:   {g.standalone_visible} not-corroborated events surfaced (want >=1)",
        f"  visibility:       {ok(g.visibility_violations)} (no event dropped)",
        f"  counts:           {ok(g.count_violations)} (corro/standalone/unresolved vs label)",
        f"  digest-invar.:    {ok(g.digest_violations)} (join read-only w.r.t. the digest)",
        f"  charter:          {len(g.charter_violations)} violations (max 0)",
    ]
    if v.insufficient:
        lines.append(f"  GATE: INSUFFICIENT — {'; '.join(v.reasons)} (never a pass)")
    elif v.passed:
        lines.append(
            "  GATE: PASSED — the discrete-event join is certified faithful (deterministic);"
            " the events lane may be surfaced behind this gate (doc 11 §3.5)"
        )
    else:
        lines.append(f"  GATE: FAILED — {'; '.join(v.reasons)}")
    return "\n".join(lines)


def load_rows(path: str | Path) -> list[dict]:
    return [json.loads(line) for line in Path(path).read_text().splitlines() if line.strip()]


def load_label(path: str | Path) -> dict:
    return json.loads(Path(path).read_text())


def main() -> int:
    ap = argparse.ArgumentParser(description="events-corroboration gate (v3 T-C)")
    ap.add_argument(
        "--events", required=True, nargs="+", help="per-scenario JSONL corroboration rows"
    )
    ap.add_argument(
        "--labels",
        required=True,
        nargs="+",
        help="ground-truth oracle JSON(s), paired with --events",
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
