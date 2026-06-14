"""Cross-service cascade backtest gate (doc 15 phase D / doc 11 §3.5).

The gate between the v2 cross-service cascade and the operator. The cascade is a
DETERMINISTIC, MEASURED+AUTHORED structural claim: each eval tick it maps the
tick's findings to their degraded workloads, walks the captured observed-flow
topology BACKWARD to the impacted callers, and joins ONE authored relation. No
operator-visible class ships before its backtest gate passes (doc 11 §3.5); this
gate is that exit for the cross-service surface (doc 15 phase F).

Substrate (the replay guarantee, doc 05 §3.5): the cascade is a pure function of
(findings, flow topology, relation, window) — all pinned in a capture bundle — so
`replay -crossservice` re-computes, per tick, byte-identically what obsd surfaced
live. The Go pass writes one `TickCrossService` JSONL record per tick; this scorer
grades those records against per-bundle GROUND TRUTH (which workload was degraded
+ its real callers).

Inputs (paired, one of each per bundle):
  - events JSONL from `replay -crossservice`: per-tick {fired, root, impacted,
    degraded, charterClean} records.
  - a label JSON: the bundle's ground truth — scenario (positive/negative),
    expect_fire, and (for positives) the root workload + its recoverable callers.

Scores (doc 11 §3.5 discipline, adapted to a deterministic class):
  - ROOT accuracy: every fired tick must name the labeled degraded root EXACTLY.
  - CALLER recall / precision: the named impacted set must equal the recoverable
    callers — no missed caller, NO phantom caller (a phantom = a mis-join).
  - NO-FALSE-CASCADE: a fired chain where the scenario expected silence is the
    trust-killing failure; zero tolerance.
  - CHARTER: every rendered chain stays free of causal-claim tokens.

Unlike the forecast gate, the accuracy floors are EXACT (1.0 / zero): this is a
measured structural function, not a statistical band — naming the wrong root or a
phantom caller is a bug, not noise. Sufficiency mirrors the forecast gate:
INSUFFICIENT (thin corpus) is never a pass — a class ships on evidence, not on
its absence (≥2 independent scenarios, enough fired + quiet ticks).
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass, field
from pathlib import Path

# --- v1 gate criteria (cross-service cascade class) -------------------------
ROOT_ACCURACY_MIN = 1.0     # every fired tick names the labeled degraded root
CALLER_RECALL_MIN = 1.0     # every ground-truth (recoverable) caller is named
CALLER_PRECISION_MIN = 1.0  # no phantom caller — a non-caller named impacted is a mis-join
FALSE_CASCADE_MAX = 0       # zero false cascades on negative evidence (trust-killer)
# Charter violations are likewise absolute-zero (a fused causal token).

# Sufficiency — INSUFFICIENT is never a pass (the forecast gate's hard-won
# discipline, doc 11 §3.5 / corpus B1). A deterministic class still needs enough
# INDEPENDENT evidence: two distinct topologies so a single lucky fan-in cannot
# carry the verdict, enough fired ticks to have actually exercised the walk, and
# enough quiet ticks to substantiate the no-false-cascade claim.
MIN_FIRED_TICKS = 5      # positive fired ticks scored
MIN_DISTINCT_ROOTS = 2   # independent scenarios/topologies that produced a chain
MIN_NEGATIVE_TICKS = 5   # quiet ticks observed (no-false-cascade evidence)


@dataclass
class BundleReport:
    """Cross-service scores for one labeled bundle."""

    bundle: str
    scenario: str          # "positive" | "negative"
    expect_fire: bool
    root_truth: str = ""
    callers_truth: frozenset[str] = frozenset()

    ticks: int = 0
    fired_ticks: int = 0
    root_correct: int = 0
    caller_recall_sum: float = 0.0
    caller_precision_sum: float = 0.0
    charter_violations: int = 0
    false_cascades: int = 0  # fired ticks where the scenario expected silence

    wrong_roots: dict = field(default_factory=dict)
    phantom_callers: dict = field(default_factory=dict)
    missed_callers: dict = field(default_factory=dict)

    @property
    def positive(self) -> bool:
        return self.expect_fire

    @property
    def scored_positive_ticks(self) -> int:
        return self.fired_ticks if self.positive else 0


def score_bundle(events: list[dict], label: dict) -> BundleReport:
    """Grade one bundle's per-tick cascade records against its ground truth."""
    scenario = label.get("scenario", "positive")
    rep = BundleReport(
        bundle=str(label.get("bundle", "?")),
        scenario=scenario,
        expect_fire=bool(label.get("expect_fire", scenario == "positive")),
        root_truth=str(label.get("root", "")),
        callers_truth=frozenset(label.get("callers", [])),
    )
    truth = set(rep.callers_truth)
    for tick in events:
        rep.ticks += 1
        if not tick.get("fired"):
            continue
        rep.fired_ticks += 1
        if not tick.get("charterClean", True):
            rep.charter_violations += 1
        if not rep.expect_fire:
            # A chain where the scenario expected silence = a FALSE cascade. Do
            # not grade root/callers against a truth that does not exist.
            rep.false_cascades += 1
            continue
        root = tick.get("root", "")
        if root == rep.root_truth:
            rep.root_correct += 1
        else:
            rep.wrong_roots[root] = rep.wrong_roots.get(root, 0) + 1
        impacted = set(tick.get("impacted", []))
        hit = impacted & truth
        if truth:
            rep.caller_recall_sum += len(hit) / len(truth)
            for miss in truth - impacted:
                rep.missed_callers[miss] = rep.missed_callers.get(miss, 0) + 1
        # A fired chain always carries ≥1 link, so impacted is non-empty; guard
        # anyway. Precision punishes phantom callers (impacted not in the truth).
        if impacted:
            rep.caller_precision_sum += len(hit) / len(impacted)
            for ph in impacted - truth:
                rep.phantom_callers[ph] = rep.phantom_callers.get(ph, 0) + 1
    return rep


@dataclass
class GateReport:
    bundles: list[BundleReport]

    @property
    def positives(self) -> list[BundleReport]:
        return [b for b in self.bundles if b.positive]

    @property
    def negatives(self) -> list[BundleReport]:
        return [b for b in self.bundles if not b.positive]

    @property
    def fired_ticks(self) -> int:
        return sum(b.fired_ticks for b in self.positives)

    @property
    def root_accuracy(self) -> float:
        f = self.fired_ticks
        return sum(b.root_correct for b in self.positives) / f if f else float("nan")

    @property
    def caller_recall(self) -> float:
        f = self.fired_ticks
        return sum(b.caller_recall_sum for b in self.positives) / f if f else float("nan")

    @property
    def caller_precision(self) -> float:
        f = self.fired_ticks
        return sum(b.caller_precision_sum for b in self.positives) / f if f else float("nan")

    @property
    def false_cascades(self) -> int:
        return sum(b.false_cascades for b in self.bundles)

    @property
    def charter_violations(self) -> int:
        return sum(b.charter_violations for b in self.bundles)

    @property
    def negative_ticks(self) -> int:
        return sum(b.ticks for b in self.negatives)

    @property
    def distinct_roots(self) -> set[str]:
        return {b.root_truth for b in self.positives if b.fired_ticks > 0 and b.root_truth}

    @property
    def positive_bundles_no_fire(self) -> list[str]:
        return [b.bundle for b in self.positives if b.fired_ticks == 0]


@dataclass
class GateVerdict:
    passed: bool
    insufficient: bool
    reasons: list


def gate(g: GateReport) -> GateVerdict:
    """The doc 11 §3.5 gate for the cross-service cascade class. INSUFFICIENT
    (thin corpus) is never a pass; the accuracy floors are exact."""
    insuff: list[str] = []
    if g.fired_ticks < MIN_FIRED_TICKS:
        insuff.append(
            f"insufficient positive evidence: {g.fired_ticks} fired ticks < {MIN_FIRED_TICKS}"
        )
    if len(g.distinct_roots) < MIN_DISTINCT_ROOTS:
        insuff.append(
            f"insufficient scenario diversity: {len(g.distinct_roots)} distinct firing"
            f" topologies < {MIN_DISTINCT_ROOTS} — one lucky fan-in cannot carry the gate"
        )
    if g.negative_ticks < MIN_NEGATIVE_TICKS:
        insuff.append(
            f"insufficient negative evidence: {g.negative_ticks} quiet ticks < {MIN_NEGATIVE_TICKS}"
            " — the no-false-cascade claim needs a quiet cluster to have been observed"
        )
    for b in g.positive_bundles_no_fire:
        insuff.append(
            f"positive bundle '{b}' produced 0 fired ticks — its scenario yielded no"
            " gradeable cascade (capture did not induce the degradation)"
        )
    if insuff:
        return GateVerdict(False, True, insuff)

    reasons: list[str] = []
    if g.root_accuracy < ROOT_ACCURACY_MIN:
        wrong = {k: v for b in g.positives for k, v in b.wrong_roots.items()}
        reasons.append(
            f"root accuracy {g.root_accuracy:.3f} < {ROOT_ACCURACY_MIN}: a deterministic"
            f" structural fan-in named the wrong root {wrong or ''}"
        )
    if g.caller_recall < CALLER_RECALL_MIN:
        missed = {k: v for b in g.positives for k, v in b.missed_callers.items()}
        reasons.append(
            f"caller recall {g.caller_recall:.3f} < {CALLER_RECALL_MIN}: a recoverable"
            f" ground-truth caller went unnamed {missed or ''}"
        )
    if g.caller_precision < CALLER_PRECISION_MIN:
        phantom = {k: v for b in g.positives for k, v in b.phantom_callers.items()}
        reasons.append(
            f"caller precision {g.caller_precision:.3f} < {CALLER_PRECISION_MIN}: a phantom"
            f" caller was named impacted (a mis-join) {phantom or ''}"
        )
    if g.false_cascades > FALSE_CASCADE_MAX:
        reasons.append(
            f"false cascades {g.false_cascades} > {FALSE_CASCADE_MAX}: a chain fired where"
            " the scenario expected silence (the trust-killing failure)"
        )
    if g.charter_violations > 0:
        reasons.append(
            f"charter violations {g.charter_violations}: a rendered chain carried a"
            " causal-claim token (the join was fused, not joined)"
        )
    return GateVerdict(passed=not reasons, insufficient=False, reasons=reasons)


def render(g: GateReport, verdict: GateVerdict) -> str:
    lines = ["cross-service cascade backtest — class: cross_service_cascade"]
    for b in g.bundles:
        if b.positive:
            rec = b.caller_recall_sum / b.fired_ticks if b.fired_ticks else float("nan")
            prec = b.caller_precision_sum / b.fired_ticks if b.fired_ticks else float("nan")
            lines.append(
                f"  [{b.bundle}] positive · {b.fired_ticks}/{b.ticks} ticks fired ·"
                f" root {b.root_correct}/{b.fired_ticks} correct (want {b.root_truth or '?'})"
                f" · caller recall {rec:.2f} precision {prec:.2f}"
            )
            if b.wrong_roots:
                lines.append(f"      wrong roots: {b.wrong_roots}")
            if b.missed_callers:
                lines.append(f"      missed callers: {b.missed_callers}")
            if b.phantom_callers:
                lines.append(f"      phantom callers: {b.phantom_callers}")
        else:
            lines.append(
                f"  [{b.bundle}] negative · {b.ticks} ticks observed ·"
                f" {b.false_cascades} false cascades (want 0)"
            )
        if b.charter_violations:
            lines.append(f"      CHARTER violations: {b.charter_violations}")
    lines += [
        f"  aggregate: {g.fired_ticks} fired ticks over {len(g.distinct_roots)} distinct"
        f" firing topologies; {g.negative_ticks} quiet ticks",
        f"  root accuracy:    {g.root_accuracy:.3f} (floor {ROOT_ACCURACY_MIN})",
        f"  caller recall:    {g.caller_recall:.3f} (floor {CALLER_RECALL_MIN})",
        f"  caller precision: {g.caller_precision:.3f} (floor {CALLER_PRECISION_MIN})",
        f"  false cascades:   {g.false_cascades} (max {FALSE_CASCADE_MAX})",
        f"  charter:          {g.charter_violations} causal-token violations (max 0)",
    ]
    if verdict.insufficient:
        lines.append(f"  GATE: INSUFFICIENT — {'; '.join(verdict.reasons)} (never a pass)")
    elif verdict.passed:
        lines.append("  GATE: PASSED — class may become operator-visible"
                     " (doc 11 §3.5 / doc 15 phase F)")
    else:
        lines.append(f"  GATE: FAILED — {'; '.join(verdict.reasons)}")
    return "\n".join(lines)


def load_events(path: str | Path) -> list[dict]:
    return [json.loads(line) for line in Path(path).read_text().splitlines() if line.strip()]


def load_label(path: str | Path) -> dict:
    return json.loads(Path(path).read_text())


def main() -> int:
    ap = argparse.ArgumentParser(description="cross-service cascade backtest gate (doc 15 D)")
    ap.add_argument("--events", required=True, nargs="+",
                    help="JSONL file(s) from `replay -crossservice` — one per bundle")
    ap.add_argument("--labels", required=True, nargs="+",
                    help="ground-truth JSON label(s), paired with --events")
    args = ap.parse_args()
    if len(args.events) != len(args.labels):
        ap.error("--events and --labels must pair up")

    bundles = [
        score_bundle(load_events(ev), load_label(lbl))
        for ev, lbl in zip(args.events, args.labels, strict=True)
    ]
    g = GateReport(bundles=bundles)
    verdict = gate(g)
    print(render(g, verdict))
    return 0 if verdict.passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
