"""Harness-wired regression gates for graph governance (doc 12 M3, with 11 M7).

The governance workflow scales required regression to the change class (doc 12 §3.2);
this module RUNS those gates and writes the ledger the Go workflow (obsd/internal/
governance) consumes. The rule (doc 12 §3.3, M3 exit): a required gate that did not
run, or ran and failed, BLOCKS the release. The harness can block; it cannot approve.

Each gate maps to a REAL, committed, offline-runnable check — never a stub:

    authoring-lints        graphlint -strict -release-latest   (schema + immutability)
    binding-qa             go test ./obsd/internal/binding/    (false-equivalence catcher, 04 M3)
    falsification          pytest tests/test_sensitivity.py    (phenomenon precision/recall, 11)
    replay-diff            pytest tests/test_replay_determinism.py  (byte-identical replay)
    shipped-class-backtest pytest tests/test_forecast_gate.py  (working-set→OOM gate, 09 M3)

Usage (from the harness dir, so the pytest gates resolve):
    uv run python -m harness.governance run-gates --class behavioural-medium --out ledger.json
    uv run python -m harness.governance run-gates --proposal ../proposal.yaml --out ledger.json
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from dataclasses import dataclass, field
from datetime import UTC, datetime
from pathlib import Path

# Repo root: harness/src/harness/governance.py -> parents[3] is the repo root.
REPO_ROOT = Path(__file__).resolve().parents[3]
HARNESS_DIR = REPO_ROOT / "harness"


@dataclass
class GateSpec:
    """One regression gate: the command that decides it, and where it runs."""

    name: str
    argv: list[str]
    cwd: Path


# The five gates, each a real check. Go gates run at the repo root; pytest gates run
# in the harness dir. The pytest gates invoke the already-committed regression suites.
GATES: dict[str, GateSpec] = {
    "authoring-lints": GateSpec(
        "authoring-lints",
        ["go", "run", "./tools/graphlint", "-strict", "-release-latest", "ontology/releases"],
        REPO_ROOT,
    ),
    "binding-qa": GateSpec(
        "binding-qa",
        ["go", "test", "./obsd/internal/binding/"],
        REPO_ROOT,
    ),
    "falsification": GateSpec(
        "falsification",
        ["python", "-m", "pytest", "-q", "tests/test_sensitivity.py"],
        HARNESS_DIR,
    ),
    "replay-diff": GateSpec(
        "replay-diff",
        ["python", "-m", "pytest", "-q", "tests/test_replay_determinism.py"],
        HARNESS_DIR,
    ),
    "shipped-class-backtest": GateSpec(
        "shipped-class-backtest",
        ["python", "-m", "pytest", "-q", "tests/test_forecast_gate.py"],
        HARNESS_DIR,
    ),
}

# Required gates per change class (MUST mirror governance.RequiredGates in Go — the
# two are cross-checked by the governance integration exercise).
REQUIRED: dict[str, list[str]] = {
    "additive-low": ["authoring-lints", "binding-qa"],
    "behavioural-medium": ["authoring-lints", "binding-qa", "falsification", "replay-diff"],
    "normative-high": [
        "authoring-lints",
        "binding-qa",
        "falsification",
        "replay-diff",
        "shipped-class-backtest",
    ],
}


@dataclass
class GateResult:
    gate: str
    status: str  # passed | failed
    detail: str
    ran_at: str
    command: str


@dataclass
class Ledger:
    proposal: str
    cls: str
    results: list[GateResult] = field(default_factory=list)

    def to_json(self) -> str:
        return json.dumps(
            {
                "proposal": self.proposal,
                "class": self.cls,
                "results": [
                    {
                        "gate": r.gate,
                        "status": r.status,
                        "detail": r.detail,
                        "ran_at": r.ran_at,
                        "command": r.command,
                    }
                    for r in self.results
                ],
            },
            indent=2,
        )


def _run_gate(spec: GateSpec, *, dry_run: bool) -> GateResult:
    now = datetime.now(UTC).isoformat()
    cmd = " ".join(spec.argv)
    if dry_run:
        # A dry run records the gate as "dry-run" — NOT "passed". The Go verifier
        # treats "dry-run" as a release-block, so a dry-run ledger (a planning aid)
        # can never be mistaken for proof the gate actually ran (the harness's job
        # is to block; a dry run executed nothing).
        return GateResult(spec.name, "dry-run", "dry-run (not executed)", now, cmd)
    try:
        proc = subprocess.run(
            spec.argv,
            cwd=spec.cwd,
            capture_output=True,
            text=True,
            timeout=900,
        )
    except (subprocess.TimeoutExpired, FileNotFoundError) as e:
        return GateResult(spec.name, "failed", f"gate did not complete: {e}", now, cmd)
    status = "passed" if proc.returncode == 0 else "failed"
    tail = (proc.stdout + proc.stderr).strip().splitlines()
    detail = tail[-1] if tail else f"exit {proc.returncode}"
    return GateResult(spec.name, status, detail[:400], now, cmd)


def _field_from_proposal(path: Path, field: str) -> str | None:
    # Minimal YAML scrape (no pyyaml dep in base): find a top-level `<field>:` line.
    for line in path.read_text().splitlines():
        s = line.strip()
        if s.startswith(field + ":"):
            return s.split(":", 1)[1].strip().strip("\"'")
    return None


def _class_from_proposal(path: Path) -> str:
    cls = _field_from_proposal(path, "class")
    if cls is None:
        raise SystemExit(f"proposal {path} has no 'class:' field")
    return cls


def run_gates(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(prog="harness.governance run-gates")
    ap.add_argument("--class", dest="cls", help="additive-low|behavioural-medium|normative-high")
    ap.add_argument("--proposal", help="proposal YAML (reads its 'class' if --class omitted)")
    ap.add_argument("--out", required=True, help="ledger JSON output path")
    ap.add_argument(
        "--dry-run", action="store_true", help="record gates as passed without running them"
    )
    args = ap.parse_args(argv)

    cls = args.cls
    proposal_name = "(none)"
    if args.proposal:
        p = Path(args.proposal)
        # The ledger's proposal identity is the proposal's `release:` field (what the
        # Go workflow binds to), NOT the filename — so a ledger pins the exact release
        # it was run for (doc 12 §3.1).
        proposal_name = _field_from_proposal(p, "release") or p.stem
        if not cls:
            cls = _class_from_proposal(p)
    if not cls:
        ap.error("either --class or --proposal is required")
    if cls not in REQUIRED:
        ap.error(f"unknown class {cls!r} (additive-low|behavioural-medium|normative-high)")

    ledger = Ledger(proposal=proposal_name, cls=cls)
    all_passed = True
    for gate_name in REQUIRED[cls]:
        result = _run_gate(GATES[gate_name], dry_run=args.dry_run)
        ledger.results.append(result)
        marker = "PASS" if result.status == "passed" else "FAIL"
        print(f"  [{marker}] {gate_name}: {result.detail}", file=sys.stderr)
        if result.status != "passed":
            all_passed = False

    Path(args.out).write_text(ledger.to_json())
    if args.dry_run:
        print(
            f"wrote DRY-RUN ledger {args.out}: {len(ledger.results)} gate(s) for {cls} — "
            f"nothing executed; the Go verifier will BLOCK this ledger (planning aid only)",
            file=sys.stderr,
        )
        return 0  # the dry-run command itself succeeded; the ledger is non-approving
    print(
        f"wrote ledger {args.out}: {len(ledger.results)} gate(s) for {cls} — "
        f"{'ALL PASSED' if all_passed else 'BLOCKED'}",
        file=sys.stderr,
    )
    # The harness BLOCKS (nonzero) on any failed gate; it never approves.
    return 0 if all_passed else 1


def main(argv: list[str] | None = None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    if not argv or argv[0] != "run-gates":
        print(__doc__, file=sys.stderr)
        return 2
    return run_gates(argv[1:])


if __name__ == "__main__":
    raise SystemExit(main())
