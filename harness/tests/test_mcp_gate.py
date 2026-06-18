"""Regression + adversarial tests for the MCP read-only harness gate (v3 T-A)."""

from __future__ import annotations

import copy
import json
from pathlib import Path

from harness.mcp_gate import gate, score

REPO = Path(__file__).resolve().parents[2]


def good_ledger_doc() -> dict:
    silent = [
        {
            "entityCei": "pvc|shop|data-0",
            "entity": "PVC",
            "ruleId": "THR_PVC",
            "metric": "used",
            "state": "bound",
            "reasonClass": "no-stream-key",
            "reason": "no stream key",
        },
        {
            "entityCei": "i|c||Pod|p|u",
            "entity": "Container",
            "ruleId": "THR_MEM",
            "metric": "ws",
            "state": "bound",
            "reasonClass": "unbounded",
            "reason": "unbounded: not declared",
        },
    ]
    ledger = {
        "class": "MEASURED",
        "available": True,
        "summary": {
            "totalPairs": 5,
            "watched": 3,
            "silent": 2,
            "byReason": {"no-stream-key": 1, "unbounded": 1},
        },
        "silent": silent,
    }
    per = {
        "totalBindings": 5,
        "instantiated": 5,
        "configBound": 3,
        "defaultBound": 1,
        "unbounded": 1,
        "outOfScope": 0,
        "unresolved": 0,
    }
    return {"ledgerA": ledger, "ledgerB": copy.deepcopy(ledger), "perRule": per}


def good_advisory_rows() -> list[dict]:
    rows = []
    for reg, n in (("causal", 3), ("future-certainty", 3), ("fusion", 3)):
        for i in range(n):
            rows.append(
                {
                    "text": f"{reg} {i}",
                    "label": "banned",
                    "register": reg,
                    "refused": True,
                    "withheld": False,
                    "textEmitted": "",
                }
            )
    for i in range(3):
        rows.append(
            {
                "text": f"clean {i}",
                "label": "clean",
                "refused": False,
                "withheld": True,
                "textEmitted": "",
            }
        )
    return rows


def test_good_corpus_passes():
    v = gate(score(good_ledger_doc(), good_advisory_rows()))
    assert v.passed and not v.insufficient, v.reasons


def test_nondeterministic_ledger_fails():
    doc = good_ledger_doc()
    doc["ledgerB"]["summary"]["watched"] = 99  # B diverges from A
    v = gate(score(doc, good_advisory_rows()))
    assert not v.passed and not v.insufficient
    assert any("deterministic" in r for r in v.reasons)


def test_dropped_silent_pair_fails_completeness():
    doc = good_ledger_doc()
    doc["ledgerA"]["silent"].pop()  # drop a silent row but keep summary.silent=2
    doc["ledgerB"] = copy.deepcopy(doc["ledgerA"])
    v = gate(score(doc, good_advisory_rows()))
    assert not v.passed and not v.insufficient
    assert any("completeness" in r for r in v.reasons)


def test_reconciliation_break_fails():
    doc = good_ledger_doc()
    doc["perRule"]["unbounded"] = 5  # disagree with the ledger's 1
    doc["ledgerB"] = copy.deepcopy(doc["ledgerA"])
    v = gate(score(doc, good_advisory_rows()))
    assert not v.passed and not v.insufficient
    assert any("reconciliation" in r for r in v.reasons)


def test_missed_honeypot_fails_recall():
    rows = good_advisory_rows()
    rows[0]["refused"] = False  # a banned draft slips the guard
    v = gate(score(good_ledger_doc(), rows))
    assert not v.passed and not v.insufficient
    assert any("refusal-recall" in r for r in v.reasons)


def test_false_block_fails():
    rows = good_advisory_rows()
    rows[-1]["refused"] = True  # a clean draft wrongly refused
    v = gate(score(good_ledger_doc(), rows))
    assert not v.passed and not v.insufficient
    assert any("false-block" in r for r in v.reasons)


def test_content_leak_fails():
    rows = good_advisory_rows()
    rows[-1]["textEmitted"] = "leaked"  # withheld but content present
    v = gate(score(good_ledger_doc(), rows))
    assert not v.passed and not v.insufficient
    assert any("content leak" in r for r in v.reasons)


def test_ledger_charter_violation_fails():
    doc = good_ledger_doc()
    doc["ledgerA"]["silent"][0]["reason"] = "the leak is caused by the deploy"
    doc["ledgerB"] = copy.deepcopy(doc["ledgerA"])
    v = gate(score(doc, good_advisory_rows()))
    assert not v.passed and not v.insufficient
    assert any("charter" in r for r in v.reasons)


def test_thin_corpus_insufficient():
    rows = good_advisory_rows()[:4]  # too few banned, registers incomplete
    v = gate(score(good_ledger_doc(), rows))
    assert not v.passed and v.insufficient


def test_live_frozen_corpus_passes():
    ledger_path = REPO / "corpus" / "mcp" / "silence-ledger.json"
    advisory_path = REPO / "corpus" / "mcp" / "advisory-drafts.jsonl"
    if not ledger_path.exists() or not advisory_path.exists():
        import pytest

        pytest.fail("frozen MCP corpus missing; REGEN_MCP_CORPUS=1 (fail not skip, #5)")
    doc = json.loads(ledger_path.read_text())
    rows = [json.loads(line) for line in advisory_path.read_text().splitlines() if line.strip()]
    v = gate(score(doc, rows))
    assert v.passed and not v.insufficient, v.reasons
