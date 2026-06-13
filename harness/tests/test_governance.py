"""Regression for the governance gate runner (doc 12 M3).

These pin the gate mapping + the ledger contract the Go workflow consumes. They run
in --dry-run (the real gates shell out to go/pytest and are exercised by the
governance integration exercise, not this unit test)."""

from __future__ import annotations

import json
from pathlib import Path

from harness.governance import REQUIRED, main, run_gates


def test_required_gates_scale_with_class():
    # Must mirror governance.RequiredGates in Go (cross-checked by the exercise).
    assert REQUIRED["additive-low"] == ["authoring-lints", "binding-qa"]
    assert len(REQUIRED["behavioural-medium"]) == 4
    assert len(REQUIRED["normative-high"]) == 5
    # Supersets: each higher class contains every lower-class gate.
    assert set(REQUIRED["additive-low"]) <= set(REQUIRED["behavioural-medium"])
    assert set(REQUIRED["behavioural-medium"]) <= set(REQUIRED["normative-high"])


def test_dry_run_ledger(tmp_path: Path):
    out = tmp_path / "ledger.json"
    rc = run_gates(["--class", "normative-high", "--out", str(out), "--dry-run"])
    assert rc == 0
    ledger = json.loads(out.read_text())
    assert ledger["class"] == "normative-high"
    assert len(ledger["results"]) == 5
    # A dry-run records "dry-run", NOT "passed" — so the Go verifier blocks a dry-run
    # ledger (it can never masquerade as proof a gate actually ran).
    assert all(r["status"] == "dry-run" for r in ledger["results"])
    # Every result carries the auditability fields the Go side reads.
    for r in ledger["results"]:
        assert r["gate"] and r["command"] and r["ran_at"]


def test_class_read_from_proposal(tmp_path: Path):
    p = tmp_path / "proposal.yaml"
    p.write_text("release: v0.4.0\nclass: behavioural-medium\nauthor: x\n")
    out = tmp_path / "ledger.json"
    rc = run_gates(["--proposal", str(p), "--out", str(out), "--dry-run"])
    assert rc == 0
    ledger = json.loads(out.read_text())
    assert ledger["class"] == "behavioural-medium"
    assert len(ledger["results"]) == 4


def test_main_requires_subcommand():
    assert main([]) == 2
    assert main(["bogus"]) == 2
