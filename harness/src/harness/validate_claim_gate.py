"""validate_claim referee gate (v3 T-D / doc 01 / doc 11 §3.5).

Certifies the deterministic LLM-claim referee over a FROZEN corpus, offline. The
verdicts are produced by the REAL api.ValidateClaim (Go regen) in two modes — full
(both backstops) and structural-only (the charter denylist OFF) — so this gate grades
the actual referee, not a re-implementation.

The cardinal rule is FALSE-BLOCK == 0: a referee that flags a TRUE statement is worse
than none. So the headline floor is zero false positives on the legitimate set —
including the trap cases that share vocabulary with fabrications (a clumsy causal
restatement of a REAL authored relation, a legitimate banded projection, a true
MEASURED crossing).

Floors:
  1. FALSE-BLOCK == 0   — ABSOLUTE. One flagged legit claim fails the gate.
  2. RECALL >= 0.90     — fraction of fabrications the full referee flags.
  3. PER-CATEGORY >= 1  — each fabrication category has >=1 caught (no blind category).
  4. MUTATION >= 0.80   — with the substring denylist OFF, the STRUCTURAL backstop alone
                          still catches >=80% of relation-absent + structural-honeypot
                          fabrications (it is not merely riding the denylist).
  5. LABELLED-BEST-EFFORT == true on every verdict; NEVER-BLOCK — no verdict carries a
                          block directive (structural: the schema has no such field).

A substantive VIOLATION fails the gate. Absent any violation, a corpus missing a
required category is INSUFFICIENT — never a pass.
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass, field
from pathlib import Path

FAB_CATEGORIES = {"relation-absent", "class-fusion", "future-certainty", "structural-honeypot"}
REQUIRED_CATEGORIES = {"legit"} | FAB_CATEGORIES
# The structural backstop is graded (mutation mode) only where it is the intended
# mechanism: relation-absent causation. future-certainty is a denylist concern.
MUTATION_CATEGORIES = {"relation-absent", "structural-honeypot"}
RECALL_FLOOR = 0.90
MUTATION_FLOOR = 0.80


@dataclass
class GateReport:
    categories: set = field(default_factory=set)
    n_legit: int = 0
    n_fab: int = 0
    false_blocks: list = field(default_factory=list)
    fab_caught: int = 0
    per_category_caught: dict = field(default_factory=dict)
    mutation_total: int = 0
    mutation_caught: int = 0
    not_best_effort: list = field(default_factory=list)
    blocked: list = field(default_factory=list)

    def recall(self) -> float:
        return 1.0 if self.n_fab == 0 else self.fab_caught / self.n_fab

    def mutation_recall(self) -> float:
        return 1.0 if self.mutation_total == 0 else self.mutation_caught / self.mutation_total


def score(records: list[dict]) -> GateReport:
    g = GateReport()
    for r in records:
        cat = r.get("category", "")
        g.categories.add(cat)
        full_flagged = bool(r["full"]["flagged"])
        struct_flagged = bool(r["structural"]["flagged"])

        # honesty + never-block
        if not r.get("labelledBestEffort", False):
            g.not_best_effort.append(r["id"])
        if "blocked" in r:  # the schema must carry no block directive
            g.blocked.append(r["id"])

        if not r["expectFlagged"]:
            g.n_legit += 1
            if full_flagged:
                g.false_blocks.append(f"{r['id']} ({cat})")
            continue

        # a fabrication
        g.n_fab += 1
        g.per_category_caught.setdefault(cat, 0)
        if full_flagged:
            g.fab_caught += 1
            g.per_category_caught[cat] += 1
        if cat in MUTATION_CATEGORIES:
            g.mutation_total += 1
            if struct_flagged:
                g.mutation_caught += 1
    return g


@dataclass
class GateVerdict:
    passed: bool
    insufficient: bool
    reasons: list


def gate(g: GateReport) -> GateVerdict:
    reasons: list[str] = []
    if g.false_blocks:
        reasons.append("FALSE-BLOCK (ABSOLUTE): legit claims flagged: " + "; ".join(g.false_blocks))
    if g.recall() < RECALL_FLOOR:
        reasons.append(f"RECALL {g.recall():.3f} < {RECALL_FLOOR}: {g.fab_caught}/{g.n_fab}")
    missing_cat = {
        c for c in FAB_CATEGORIES if g.per_category_caught.get(c, 0) < 1 and any_in_category(g, c)
    }
    if missing_cat:
        reasons.append(f"per-category recall 0 in: {sorted(missing_cat)} (a blind category)")
    if g.mutation_recall() < MUTATION_FLOOR:
        reasons.append(
            f"MUTATION {g.mutation_recall():.3f} < {MUTATION_FLOOR}: structural backstop weak "
            f"({g.mutation_caught}/{g.mutation_total} caught, denylist off)"
        )
    if g.not_best_effort:
        reasons.append("not labelled best-effort: " + "; ".join(g.not_best_effort))
    if g.blocked:
        reasons.append(
            "NEVER-BLOCK broken (a verdict carried a block directive): " + "; ".join(g.blocked)
        )
    if reasons:
        return GateVerdict(passed=False, insufficient=False, reasons=reasons)

    missing = REQUIRED_CATEGORIES - g.categories
    if missing:
        return GateVerdict(
            passed=False,
            insufficient=True,
            reasons=[f"missing required category(ies): {sorted(missing)}"],
        )
    return GateVerdict(passed=True, insufficient=False, reasons=[])


def any_in_category(g: GateReport, cat: str) -> bool:
    # per_category_caught keys are only created for categories that appeared as fabs.
    return cat in g.per_category_caught


def render(g: GateReport, v: GateVerdict) -> str:
    ok = lambda bad: "OK" if not bad else "BROKEN"  # noqa: E731
    per = {c: g.per_category_caught.get(c, 0) for c in sorted(FAB_CATEGORIES)}
    lines = [
        "validate-claim referee gate — class: deterministic_referee",
        f"  categories:       {sorted(g.categories)}",
        f"  legit / fab:      {g.n_legit} legit, {g.n_fab} fabrications",
        f"  FALSE-BLOCK:      {len(g.false_blocks)} (max 0, ABSOLUTE)",
        f"  recall:           {g.recall():.3f} (>= {RECALL_FLOOR}; {g.fab_caught}/{g.n_fab})",
        f"  per-category:     {per}",
        f"  MUTATION struct:  {g.mutation_recall():.3f} (>= {MUTATION_FLOOR}; {g.mutation_caught}/{g.mutation_total} caught)",  # noqa: E501
        f"  best-effort:      {ok(g.not_best_effort)} (every verdict labelled)",
        f"  never-block:      {ok(g.blocked)} (no block directive in any verdict)",
    ]
    if v.insufficient:
        lines.append(f"  GATE: INSUFFICIENT — {'; '.join(v.reasons)} (never a pass)")
    elif v.passed:
        lines.append(
            "  GATE: PASSED — the referee flags fabrications without false-blocking a true claim,"
            " and the structural backstop has independent teeth; surfaceable behind this gate"
        )
    else:
        lines.append(f"  GATE: FAILED — {'; '.join(v.reasons)}")
    return "\n".join(lines)


def load_records(path: str | Path) -> list[dict]:
    return [json.loads(line) for line in Path(path).read_text().splitlines() if line.strip()]


def main() -> int:
    ap = argparse.ArgumentParser(description="validate-claim referee gate (v3 T-D)")
    ap.add_argument(
        "--verdicts",
        required=True,
        help="frozen verdicts.jsonl (REAL ValidateClaim, full+structural)",
    )
    args = ap.parse_args()
    records = load_records(args.verdicts)
    g = score(records)
    v = gate(g)
    print(render(g, v))
    return 0 if v.passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
