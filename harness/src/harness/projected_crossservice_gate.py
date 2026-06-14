"""Anticipatory (phase E) cross-service cascade backtest gate (doc 15 phase E / doc 11 §3.5).

The gate between the v2 ANTICIPATORY cross-service cascade and the operator. Where
the phase-D cascade is a MEASURED structural claim about NOW, the phase-E cascade is
a PROJECTED claim about SOON: a callee the forecast lane (TimesFM) projects to cross
its bar seeds a projected downstream-impact hypothesis to its callers over the
MEASURED flow edge + the AUTHORED relation (weakest-input rule, doc 01 — PROJECTED
nodes, MEASURED edge, AUTHORED why, each labelled, never fused). No operator-visible
class ships before its own backtest gate passes (doc 11 §3.5); this gate is that exit
for the PROJECTED lane.

Substrate: `replay -projected-crossservice` re-runs the REAL forecast funnel at every
recorded tick (against the bundle's readings + the injected model clock), seeds the
cascade from the warned callees, and emits BOTH the anticipatory (PROJECTED) chain
AND the measured (MEASURED) chain of the same tick. The forecast is non-deterministic
BY CLASS (PROJECTED), so this is NOT a digest claim; what the gate verifies is the
JOIN (deterministic) and the operator value: did the projection LEAD the measured
reality, and did it CONFIRM?

Inputs (paired, one of each per bundle):
  - events JSONL from `replay -projected-crossservice`: per-tick {warned, projFired,
    projRoot, projImpacted, projCrossAt, projEarliest, projLatest, projBandOpen,
    measFired, measRoot, measImpacted, charterClean} records.
  - a label JSON: ground truth — scenario (positive/negative), expect_proj_fire,
    and (for positives) the roots that should anticipate + their true callers.

Scores (doc 11 §3.5 discipline, adapted to a PROJECTED-leads-MEASURED class):
  - LEAD-TIME: every CONFIRMED root's anticipatory chain must fire strictly BEFORE
    its measured chain, by at least a meaningful margin (operator value is lead, not
    a one-tick coincidence). The worst confirmed lead is reported.
  - CONFIRM: a projected root must MATERIALISE — the measured cross-service cascade
    later fires on the same root within the confirm horizon. The gate needs >=2
    INDEPENDENT confirmed roots (one lucky ramp cannot carry it).
  - STRUCTURAL FIDELITY: a confirmed root's PROJECTED impacted callers must equal its
    MEASURED impacted callers (the anticipatory walk named the real fan-in — no
    phantom caller, no missed one).
  - NO-FALSE-ANTICIPATION: on negative evidence (an abrupt fault the forecast does
    NOT anticipate; a healthy cluster), zero anticipatory chains may fire.
  - CHARTER: every rendered anticipatory chain stays free of causal-claim tokens AND
    of a collapsed band (a PROJECTED crossing dressed as MEASURED certainty).

Sufficiency mirrors the forecast + phase-D gates: INSUFFICIENT (thin corpus) is never
a pass — a class ships on evidence, not its absence.
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path

# --- v1 gate criteria (anticipatory cross-service cascade class) -------------
LEAD_TIME_MIN_SECONDS = 120.0   # a confirmed root must lead its measured reality by >=2min
CONFIRM_HORIZON_SECONDS = 3600.0  # the measured reality must arrive within 1h of the projection
FALSE_ANTICIPATION_MAX = 0      # zero anticipatory fires on negative evidence (trust-killer)
# Charter violations (causal token OR collapsed band) are absolute-zero.

# Sufficiency — INSUFFICIENT is never a pass (doc 11 §3.5 / corpus B1). The PROJECTED
# lead/confirm claim needs INDEPENDENT evidence: >=2 distinct confirmed roots so a
# single lucky ramp cannot carry the verdict, enough anticipatory ticks to have
# actually exercised the projected walk, and enough quiet ticks to substantiate the
# no-false-anticipation claim.
MIN_PROJ_TICKS = 5        # anticipatory fired ticks scored (positives)
MIN_CONFIRMED_ROOTS = 2   # independent roots that anticipated AND materialised
MIN_QUIET_TICKS = 20      # ticks with no anticipatory fire (no-false-anticipation evidence)


def _parse(ts: str) -> datetime:
    # Go RFC3339Nano; Python 3.11+ fromisoformat handles the trailing Z + nanos.
    return datetime.fromisoformat(ts.replace("Z", "+00:00"))


@dataclass
class RootTrace:
    """One root's anticipatory + measured timeline within a bundle."""

    proj_first: datetime | None = None
    proj_impacted: frozenset[str] = frozenset()
    meas_ever: datetime | None = None   # EARLIEST measured fire (any order)
    meas_impacted: frozenset[str] = frozenset()  # impacted set at meas_ever
    declared: bool = False              # a labeled root of its (positive) bundle

    @property
    def confirmed(self) -> bool:
        # Anticipated AND materialised: the projection fired, AND the EARLIEST measured
        # fire is strictly AFTER the projection (PROJECTED truly LED MEASURED — a measured
        # fire that PRECEDES the projection means the reality was already there, not
        # anticipated), within the confirm horizon. Using meas_ever (not a later
        # measured fire) closes the lead-inflation hole.
        if self.proj_first is None or self.meas_ever is None:
            return False
        lead = (self.meas_ever - self.proj_first).total_seconds()
        return 0.0 < lead <= CONFIRM_HORIZON_SECONDS

    @property
    def projected_then_measured(self) -> bool:
        """Both lanes fired for this root at some point (order-agnostic) — the set
        over which structural fidelity is checkable."""
        return self.proj_first is not None and self.meas_ever is not None

    @property
    def lead_seconds(self) -> float:
        if self.proj_first is None or self.meas_ever is None:
            return float("nan")
        return (self.meas_ever - self.proj_first).total_seconds()


@dataclass
class BundleReport:
    bundle: str
    scenario: str               # "positive" | "negative"
    expect_proj_fire: bool
    roots_truth: frozenset[str] = frozenset()
    callers_truth: dict = field(default_factory=dict)  # root -> [callers]

    ticks: int = 0
    proj_ticks: int = 0
    quiet_ticks: int = 0
    charter_violations: int = 0
    false_anticipations: int = 0   # NEGATIVE bundle: any anticipatory fire (none expected)
    stray_anticipations: int = 0   # POSITIVE bundle: an anticipatory fire on an UNDECLARED root

    roots: dict = field(default_factory=dict)  # root -> RootTrace
    fidelity_mismatch: dict = field(default_factory=dict)  # root -> (proj_impacted, meas_impacted)

    @property
    def positive(self) -> bool:
        return self.expect_proj_fire

    @property
    def confirmed_roots(self) -> dict:
        """DECLARED roots of this bundle that anticipated AND materialised. Confirmation
        is restricted to the corpus's declared roots so an unrelated measured cascade
        (a coincidental same-name fire) cannot manufacture a confirmation."""
        return {r: t for r, t in self.roots.items() if t.declared and t.confirmed}


def score_bundle(events: list[dict], label: dict) -> BundleReport:
    """Grade one bundle's per-tick anticipatory+measured records against ground truth."""
    scenario = label.get("scenario", "positive")
    rep = BundleReport(
        bundle=str(label.get("bundle", "?")),
        scenario=scenario,
        expect_proj_fire=bool(label.get("expect_proj_fire", scenario == "positive")),
        roots_truth=frozenset(label.get("roots", [])),
        callers_truth={k: frozenset(v) for k, v in label.get("callers", {}).items()},
    )
    declared = set(rep.roots_truth)

    # One pass over the time-ordered ticks: per-root EARLIEST projection (+ its impacted
    # set) and EARLIEST measured fire (+ its impacted set). Confirmation/lead are derived
    # from those two earliest stamps so neither a late measured fire nor a measured fire
    # predating the projection can be credited as anticipated lead.
    ticks = sorted(events, key=lambda e: e.get("evalNow", ""))
    for tick in ticks:
        rep.ticks += 1
        proj_fired = bool(tick.get("projFired"))
        pr = tick.get("projRoot", "")
        if proj_fired:
            rep.proj_ticks += 1
            if not tick.get("charterClean", True):
                rep.charter_violations += 1
            if not rep.expect_proj_fire:
                rep.false_anticipations += 1   # negative scenario: any fire is false
            elif pr and pr not in declared:
                rep.stray_anticipations += 1   # positive scenario: fire on an undeclared root
        else:
            rep.quiet_ticks += 1

        if proj_fired and pr:
            t = rep.roots.setdefault(pr, RootTrace())
            if pr in declared:
                t.declared = True
            if t.proj_first is None:
                t.proj_first = _parse(tick["evalNow"])
                t.proj_impacted = frozenset(tick.get("projImpacted", []))

        if tick.get("measFired") and tick.get("measRoot"):
            mr = tick["measRoot"]
            t = rep.roots.setdefault(mr, RootTrace())
            if mr in declared:
                t.declared = True
            if t.meas_ever is None:
                t.meas_ever = _parse(tick["evalNow"])
                t.meas_impacted = frozenset(tick.get("measImpacted", []))

    # Structural fidelity: ANY root that both projected AND measured must have its
    # projected fan-in equal its measured fan-in (no phantom / missed caller) — checked
    # regardless of whether the lead confirmed, so a phantom caller on an unconfirmed
    # projection is still caught.
    for r, t in rep.roots.items():
        if t.projected_then_measured and t.proj_impacted != t.meas_impacted:
            rep.fidelity_mismatch[r] = (sorted(t.proj_impacted), sorted(t.meas_impacted))
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
    def proj_ticks(self) -> int:
        return sum(b.proj_ticks for b in self.positives)

    @property
    def quiet_ticks(self) -> int:
        return sum(b.quiet_ticks for b in self.bundles)

    @property
    def negative_quiet_ticks(self) -> int:
        """Quiet ticks observed in NEGATIVE scenarios specifically — the no-false-
        anticipation claim is substantiated where the forecast SHOULD have stayed
        silent, not by a positive scenario's pre-crossing background."""
        return sum(b.quiet_ticks for b in self.negatives)

    @property
    def confirmed_roots(self) -> dict:
        out: dict = {}
        for b in self.positives:
            out.update(b.confirmed_roots)
        return out

    @property
    def confirmed_roots_by_bundle(self) -> dict:
        """Confirmed declared roots grouped by their bundle — independence visibility."""
        out: dict = {}
        for b in self.positives:
            conf = list(b.confirmed_roots)
            if conf:
                out[b.bundle] = conf
        return out

    @property
    def worst_lead_seconds(self) -> float:
        leads = [t.lead_seconds for t in self.confirmed_roots.values()]
        return min(leads) if leads else float("nan")

    @property
    def stray_anticipations(self) -> int:
        return sum(b.stray_anticipations for b in self.positives)

    @property
    def false_anticipations(self) -> int:
        return sum(b.false_anticipations for b in self.bundles)

    @property
    def charter_violations(self) -> int:
        return sum(b.charter_violations for b in self.bundles)

    @property
    def fidelity_mismatches(self) -> dict:
        out: dict = {}
        for b in self.positives:
            out.update(b.fidelity_mismatch)
        return out

    @property
    def labeled_roots(self) -> set[str]:
        """The independent positive scenarios the corpus DECLARES (its design)."""
        return {r for b in self.positives for r in b.roots_truth}

    @property
    def unmet_labeled_roots(self) -> list[str]:
        """Labeled positive roots that did NOT confirm (anticipate + materialise)."""
        confirmed = set(self.confirmed_roots)
        unmet = []
        for b in self.positives:
            for r in b.roots_truth:
                if r not in confirmed:
                    unmet.append(r)
        return unmet


@dataclass
class GateVerdict:
    passed: bool
    insufficient: bool
    reasons: list


def gate(g: GateReport) -> GateVerdict:
    """The doc 11 §3.5 gate for the anticipatory cross-service cascade class.
    A substantive VIOLATION (a labeled root not confirming, sub-floor lead, broken
    fidelity, a stray/false anticipation, a charter breach) is a FAILURE and takes
    precedence over thin evidence. Absent any violation, a thin corpus is INSUFFICIENT
    — never a pass. Only a clean corpus with enough INDEPENDENT confirmed evidence passes."""
    reasons: list[str] = []
    if g.unmet_labeled_roots:
        reasons.append(
            f"labeled root(s) did not confirm (anticipate+materialise): {g.unmet_labeled_roots}"
        )
    if g.confirmed_roots and not (g.worst_lead_seconds >= LEAD_TIME_MIN_SECONDS):
        worst = {r: round(t.lead_seconds) for r, t in g.confirmed_roots.items()}
        reasons.append(
            f"worst confirmed lead {g.worst_lead_seconds:.0f}s < floor {LEAD_TIME_MIN_SECONDS:.0f}s"
            f" — a projection that does not meaningfully lead is not anticipatory value {worst}"
        )
    if g.fidelity_mismatches:
        reasons.append(
            "structural fidelity broken (projected fan-in != measured fan-in):"
            f" {g.fidelity_mismatches}"
        )
    if g.stray_anticipations > 0:
        reasons.append(
            f"stray anticipations {g.stray_anticipations}: an anticipatory chain fired on an"
            " UNDECLARED root in a positive scenario (the forecast warned a service the"
            " corpus did not expect to lead a cascade)"
        )
    if g.false_anticipations > FALSE_ANTICIPATION_MAX:
        reasons.append(
            f"false anticipations {g.false_anticipations} > {FALSE_ANTICIPATION_MAX}: an"
            " anticipatory chain fired where the scenario expected none (the forecast"
            " fabricated a crossing)"
        )
    if g.charter_violations > 0:
        reasons.append(
            f"charter violations {g.charter_violations}: a rendered anticipatory chain carried a"
            " causal-claim token or a collapsed band (PROJECTED dressed as certainty)"
        )
    if reasons:
        return GateVerdict(passed=False, insufficient=False, reasons=reasons)

    insuff: list[str] = []
    if g.proj_ticks < MIN_PROJ_TICKS:
        insuff.append(
            f"insufficient anticipatory evidence: {g.proj_ticks} projected ticks < {MIN_PROJ_TICKS}"
        )
    # Diversity is measured in CONFIRMED roots — roots that BOTH anticipated and
    # materialised — not merely declared names: >=2 so a single lucky ramp cannot
    # carry the gate. (confirmed_roots_by_bundle exposes whether they span scenarios.)
    if len(g.confirmed_roots) < MIN_CONFIRMED_ROOTS:
        insuff.append(
            f"insufficient confirmed diversity: {len(g.confirmed_roots)} confirmed root(s)"
            f" < {MIN_CONFIRMED_ROOTS} — one lucky ramp cannot carry the gate"
        )
    if g.negative_quiet_ticks < MIN_QUIET_TICKS:
        insuff.append(
            f"insufficient quiet evidence: {g.negative_quiet_ticks} negative-scenario"
            f" no-anticipation ticks < {MIN_QUIET_TICKS} — the no-false-anticipation claim"
            " needs a scenario where the forecast SHOULD have stayed silent"
        )
    if insuff:
        return GateVerdict(passed=False, insufficient=True, reasons=insuff)
    return GateVerdict(passed=True, insufficient=False, reasons=[])


def render(g: GateReport, verdict: GateVerdict) -> str:
    lines = ["anticipatory cross-service backtest — class: projected_cross_service_cascade"]
    for b in g.bundles:
        if b.positive:
            conf = b.confirmed_roots
            lines.append(
                f"  [{b.bundle}] positive · {b.proj_ticks}/{b.ticks} anticipatory ticks ·"
                f" {len(conf)} confirmed root(s)"
            )
            for r, t in sorted(b.roots.items()):
                if t.declared and t.confirmed:
                    fid = ("fan-in OK" if r not in b.fidelity_mismatch
                           else f"FAN-IN MISMATCH {b.fidelity_mismatch[r]}")
                    pf = t.proj_first.strftime('%H:%M:%S')
                    mf = t.meas_ever.strftime('%H:%M:%S')
                    lines.append(
                        f"      {r}: lead {t.lead_seconds:.0f}s (proj {pf}"
                        f" -> meas {mf}) · {fid}"
                    )
                elif t.proj_first is not None:
                    ms = t.meas_ever.strftime('%H:%M:%S') if t.meas_ever else "never"
                    pf = t.proj_first.strftime('%H:%M:%S')
                    tag = "" if (r in b.roots_truth) else " [UNDECLARED/stray]"
                    lines.append(
                        f"      {r}: anticipated {pf} but did NOT confirm"
                        f" (measured: {ms}) — open/unmaterialised{tag}"
                    )
            if b.stray_anticipations:
                lines.append(
                    f"      STRAY anticipations (undeclared roots): {b.stray_anticipations}")
        else:
            lines.append(
                f"  [{b.bundle}] negative · {b.ticks} ticks observed ·"
                f" {b.false_anticipations} false anticipations (want 0)"
            )
        if b.charter_violations:
            lines.append(f"      CHARTER violations: {b.charter_violations}")
    lines += [
        f"  aggregate: {g.proj_ticks} anticipatory ticks; {len(g.confirmed_roots)} confirmed roots"
        f" {g.confirmed_roots_by_bundle}",
        f"  worst confirmed lead: {g.worst_lead_seconds:.0f}s (floor {LEAD_TIME_MIN_SECONDS:.0f}s)",
        f"  stray anticipations:  {g.stray_anticipations} (max 0)",
        f"  false anticipations:  {g.false_anticipations} (max {FALSE_ANTICIPATION_MAX})",
        f"  negative quiet ticks: {g.negative_quiet_ticks} (floor {MIN_QUIET_TICKS})",
        f"  charter:              {g.charter_violations} violations (max 0)",
    ]
    if verdict.insufficient:
        lines.append(f"  GATE: INSUFFICIENT — {'; '.join(verdict.reasons)} (never a pass)")
    elif verdict.passed:
        lines.append("  GATE: PASSED — the PROJECTED cross-service lane may become"
                     " operator-visible (doc 11 §3.5 / doc 15 phase E)")
    else:
        lines.append(f"  GATE: FAILED — {'; '.join(verdict.reasons)}")
    return "\n".join(lines)


def load_events(path: str | Path) -> list[dict]:
    return [json.loads(line) for line in Path(path).read_text().splitlines() if line.strip()]


def load_label(path: str | Path) -> dict:
    return json.loads(Path(path).read_text())


def main() -> int:
    ap = argparse.ArgumentParser(
        description="anticipatory cross-service cascade backtest gate (doc 15 E)")
    ap.add_argument("--events", required=True, nargs="+",
                    help="JSONL file(s) from `replay -projected-crossservice` — one per bundle")
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
