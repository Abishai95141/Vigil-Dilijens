"""MCP read-only harness backtest gate (v3 T-A / doc 11 §3.5).

Certifies the T-A first prototype over a FROZEN corpus, offline, no cluster:

  1. SILENCE-LEDGER DETERMINISM  — two independent builds are byte-identical.
  2. ABSENCE-COMPLETENESS        — every (entity,variable) pair is watched or silent
                                   with a reason; nothing is dropped (totalPairs ==
                                   watched + silent == the compiler's binding count).
  3. RECONCILIATION              — the silence classes reconcile EXACTLY with the
                                   compiler's independent per-rule coverage counts
                                   (the ledger cannot pass as a convenient subset).
  4. ADVISORY TEETH + NO-LEAK    — refusal-recall == 1.0 over banned honeypots across
                                   all three registers; zero false-blocks on clean
                                   drafts; no content emitted while refused/withheld.
  5. CHARTER                     — the ledger payload carries no banned register.

Unlike the forecast gates, this gate is FULLY DETERMINISTIC — there is no model in
the loop — so it asserts exact equalities, not statistical bands. No-write-back and
non-gating are additionally asserted in Go (`go test -race ./obsd/internal/mcp/ ...`);
`just mcp-gate` runs both halves.

A substantive VIOLATION fails the gate and takes precedence over thin evidence.
Absent any violation, a thin corpus is INSUFFICIENT — never a pass.
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass, field
from pathlib import Path

# --- floors / sufficiency --------------------------------------------------

KNOWN_SILENCE_CLASSES = {"unbounded", "no-stream-key", "unresolved", "out-of-scope"}
MIN_BANNED = 9
MIN_CLEAN = 3
REQUIRED_REGISTERS = {"causal", "future-certainty", "fusion"}
MIN_SILENCE_CLASSES = 2  # a trivial single-class ledger is not enough to certify the partition

# A backstop denylist for the ledger payload's OWN register (honest: a substring
# backstop, not a proof — the Go TestSilenceLedgerCharterClean is the primary check).
LEDGER_DENYLIST = [
    "because",
    "caused by",
    "caused the",
    "root cause",
    "due to",
    "will cross",
    "will reach",
    "going to",
    "measured forecast",
    "projected and confirmed",
]


@dataclass
class GateReport:
    # determinism
    ledger_deterministic: bool = True
    # completeness
    total_pairs: int = 0
    watched: int = 0
    silent: int = 0
    completeness_breaks: list = field(default_factory=list)
    # reconciliation
    reconciliation_breaks: list = field(default_factory=list)
    silence_classes: set = field(default_factory=set)
    has_no_stream_key: bool = False
    # advisory
    banned_total: int = 0
    banned_refused: int = 0
    clean_total: int = 0
    false_blocks: int = 0
    withheld_breaks: int = 0
    content_leaks: int = 0
    registers_covered: set = field(default_factory=set)
    # charter
    charter_violations: list = field(default_factory=list)

    @property
    def refusal_recall(self) -> float:
        return self.banned_refused / self.banned_total if self.banned_total else 0.0


def score(ledger_doc: dict, advisory_rows: list[dict]) -> GateReport:
    g = GateReport()

    la, lb = ledger_doc["ledgerA"], ledger_doc["ledgerB"]
    g.ledger_deterministic = json.dumps(la, sort_keys=True) == json.dumps(lb, sort_keys=True)

    summ = la["summary"]
    by_reason = summ.get("byReason") or {}
    silent_rows = la.get("silent") or []
    per = ledger_doc["perRule"]

    g.total_pairs = summ["totalPairs"]
    g.watched = summ["watched"]
    g.silent = summ["silent"]
    g.silence_classes = {k for k, v in by_reason.items() if v}
    g.has_no_stream_key = by_reason.get("no-stream-key", 0) > 0

    # completeness
    if g.watched + g.silent != g.total_pairs:
        g.completeness_breaks.append(
            f"watched({g.watched})+silent({g.silent}) != total({g.total_pairs})"
        )
    if len(silent_rows) != g.silent:
        g.completeness_breaks.append(
            f"len(silent rows)={len(silent_rows)} != summary.silent={g.silent}"
        )
    if g.total_pairs != per["totalBindings"]:
        g.completeness_breaks.append(
            f"totalPairs({g.total_pairs}) != compiler bindings({per['totalBindings']})"
        )
    for row in silent_rows:
        if not row.get("reason"):
            g.completeness_breaks.append(
                f"silent row {row.get('entityCei')}/{row.get('ruleId')} has empty reason"
            )
        if row.get("reasonClass") not in KNOWN_SILENCE_CLASSES:
            g.completeness_breaks.append(
                f"silent row {row.get('entityCei')} unknown class {row.get('reasonClass')!r}"
            )

    # reconciliation against the compiler's own per-rule counts
    def recon(name, got, want):
        if got != want:
            g.reconciliation_breaks.append(f"{name}: ledger {got} != per-rule {want}")

    recon("unbounded", by_reason.get("unbounded", 0), per["unbounded"])
    recon("out-of-scope", by_reason.get("out-of-scope", 0), per["outOfScope"])
    recon("unresolved", by_reason.get("unresolved", 0), per["unresolved"])
    recon(
        "watched",
        g.watched,
        per["configBound"] + per["defaultBound"] - by_reason.get("no-stream-key", 0),
    )
    recon("total", g.total_pairs, per["instantiated"] + per["outOfScope"] + per["unresolved"])

    # charter scan of the ledger payload
    blob = json.dumps(la).lower()
    for phrase in LEDGER_DENYLIST:
        if phrase in blob:
            g.charter_violations.append(f"ledger payload carried banned phrase {phrase!r}")

    # advisory teeth + no-leak
    for r in advisory_rows:
        label = r.get("label")
        refused = bool(r.get("refused"))
        withheld = bool(r.get("withheld"))
        emitted = r.get("textEmitted", "")
        if label == "banned":
            g.banned_total += 1
            if refused:
                g.banned_refused += 1
                if r.get("register"):
                    g.registers_covered.add(r["register"])
        elif label == "clean":
            g.clean_total += 1
            if refused:
                g.false_blocks += 1
            if not withheld:
                g.withheld_breaks += 1
        if (refused or withheld) and emitted:
            g.content_leaks += 1

    return g


@dataclass
class GateVerdict:
    passed: bool
    insufficient: bool
    reasons: list


def gate(g: GateReport) -> GateVerdict:
    reasons: list[str] = []
    if not g.ledger_deterministic:
        reasons.append("silence ledger is NOT deterministic: two independent builds differ")
    if g.completeness_breaks:
        reasons.append("absence-completeness broken: " + "; ".join(g.completeness_breaks))
    if g.reconciliation_breaks:
        reasons.append(
            "reconciliation with per-rule coverage broken: " + "; ".join(g.reconciliation_breaks)
        )
    if g.banned_total and g.refusal_recall < 1.0:
        reasons.append(
            f"advisory refusal-recall {g.refusal_recall:.3f} < 1.000 —"
            " the charter guard let a banned register through"
        )
    if g.false_blocks > 0:
        reasons.append(
            f"advisory false-blocks {g.false_blocks} > 0 —"
            " a register-clean draft was wrongly refused"
        )
    if g.withheld_breaks > 0:
        reasons.append(
            f"withheld-discipline broken {g.withheld_breaks}:"
            " a clean draft was not withheld with the gate off"
        )
    if g.content_leaks > 0:
        reasons.append(
            f"content leak {g.content_leaks}: a refused/withheld advisory carried emitted text"
        )
    if g.charter_violations:
        reasons.append("ledger charter: " + "; ".join(g.charter_violations))
    if reasons:
        return GateVerdict(passed=False, insufficient=False, reasons=reasons)

    insuff: list[str] = []
    if g.total_pairs == 0:
        insuff.append("empty ledger: 0 (entity,variable) pairs")
    if len(g.silence_classes) < MIN_SILENCE_CLASSES:
        insuff.append(
            f"too few silence classes: {sorted(g.silence_classes)} < {MIN_SILENCE_CLASSES}"
        )
    if not g.has_no_stream_key:
        insuff.append("the no-stream-key (dark-bar) silence is not demonstrated in the corpus")
    if g.banned_total < MIN_BANNED:
        insuff.append(f"too few banned honeypots: {g.banned_total} < {MIN_BANNED}")
    if g.clean_total < MIN_CLEAN:
        insuff.append(f"too few clean drafts: {g.clean_total} < {MIN_CLEAN}")
    if g.registers_covered != REQUIRED_REGISTERS:
        insuff.append(
            f"banned registers not all covered: {sorted(g.registers_covered)}"
            f" != {sorted(REQUIRED_REGISTERS)}"
        )
    if insuff:
        return GateVerdict(passed=False, insufficient=True, reasons=insuff)
    return GateVerdict(passed=True, insufficient=False, reasons=[])


def render(g: GateReport, v: GateVerdict) -> str:
    det = "byte-identical (A==B)" if g.ledger_deterministic else "BROKEN"
    comp = "OK" if not g.completeness_breaks else "BROKEN"
    rec = "OK" if not g.reconciliation_breaks else "BROKEN"
    dark = "yes (no-stream-key present)" if g.has_no_stream_key else "NO"
    lines = [
        "MCP read-only harness backtest — class: measured_silence_ledger + advisory_shell",
        f"  silence ledger: {g.total_pairs} pairs · {g.watched} watched · {g.silent} silent"
        f" · classes {sorted(g.silence_classes)}",
        f"  determinism:    {det}",
        f"  completeness:   {comp} (every pair accounted)",
        f"  reconciliation: {rec} (vs per-rule coverage)",
        f"  dark-bar shown: {dark}",
        f"  advisory teeth: refusal-recall {g.refusal_recall:.3f} over {g.banned_total}"
        f" honeypots, registers {sorted(g.registers_covered)}",
        f"  advisory clean: {g.clean_total} drafts · false-blocks {g.false_blocks} (max 0)"
        f" · withheld-breaks {g.withheld_breaks} (max 0) · content-leaks {g.content_leaks} (max 0)",
        f"  ledger charter: {len(g.charter_violations)} violations (max 0)",
    ]
    if v.insufficient:
        lines.append(f"  GATE: INSUFFICIENT — {'; '.join(v.reasons)} (never a pass)")
    elif v.passed:
        lines.append(
            "  GATE: PASSED — the MCP read-only harness + silence ledger may be exposed"
            " (auth + ADVISORY content remain separate, later gates) (doc 11 §3.5)"
        )
    else:
        lines.append(f"  GATE: FAILED — {'; '.join(v.reasons)}")
    return "\n".join(lines)


def main() -> int:
    ap = argparse.ArgumentParser(description="MCP read-only harness backtest gate (v3 T-A)")
    ap.add_argument("--ledger", required=True, help="corpus/mcp/silence-ledger.json")
    ap.add_argument("--advisory", required=True, help="corpus/mcp/advisory-drafts.jsonl")
    args = ap.parse_args()

    ledger_doc = json.loads(Path(args.ledger).read_text())
    advisory_rows = [
        json.loads(line) for line in Path(args.advisory).read_text().splitlines() if line.strip()
    ]

    g = score(ledger_doc, advisory_rows)
    v = gate(g)
    print(render(g, v))
    return 0 if v.passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
