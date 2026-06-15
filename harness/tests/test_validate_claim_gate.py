"""Regression + adversarial tests for the validate-claim referee gate (v3 T-D)."""

from __future__ import annotations

import copy
from pathlib import Path

from harness.validate_claim_gate import gate, load_records, score

REPO = Path(__file__).resolve().parents[2]


def rec(id, category, expect, full_flag, struct_flag, classes=None, best=True):
    cl = classes or []
    return {
        "id": id,
        "category": category,
        "expectFlagged": expect,
        "labelledBestEffort": best,
        "full": {"flagged": full_flag, "matchedAuthored": False, "classes": cl},
        "structural": {"flagged": struct_flag, "matchedAuthored": False, "classes": cl},
    }


def good_records() -> list[dict]:
    return [
        rec("legit-1", "legit", False, False, False),
        rec("legit-2", "legit", False, False, False),
        rec("ra-1", "relation-absent", True, True, True, ["generated-causation"]),
        rec("ra-2", "relation-absent", True, True, True, ["generated-causation"]),
        rec("cf-1", "class-fusion", True, True, True, ["class-fusion"]),
        rec("fc-1", "future-certainty", True, True, False, ["future-certainty"]),
        rec("hp-1", "structural-honeypot", True, True, True, ["generated-causation"]),
    ]


def test_good_corpus_passes():
    v = gate(score(good_records()))
    assert v.passed and not v.insufficient, v.reasons


def test_false_block_fails_absolutely():
    r = good_records()
    r[0]["full"]["flagged"] = True  # a legit claim flagged
    v = gate(score(r))
    assert not v.passed and not v.insufficient
    assert any("FALSE-BLOCK" in x for x in v.reasons)


def test_recall_below_floor_fails():
    r = good_records()
    # miss most fabrications (keep one per category so per-category still has >=1... no:
    # drop enough to push recall < 0.9 while keeping categories present)
    for x in r:
        if x["category"] in ("relation-absent",) and x["id"] in ("ra-1", "ra-2"):
            x["full"]["flagged"] = False
    v = gate(score(r))
    assert not v.passed and not v.insufficient
    assert any("RECALL" in x or "per-category" in x for x in v.reasons)


def test_blind_category_fails():
    r = good_records()
    for x in r:
        if x["category"] == "class-fusion":
            x["full"]["flagged"] = False  # the whole category missed
    v = gate(score(r))
    assert not v.passed and not v.insufficient
    assert any("per-category" in x or "RECALL" in x for x in v.reasons)


def test_mutation_floor_fails():
    r = good_records()
    # structural backstop OFF catches nothing on relation-absent + honeypot
    for x in r:
        if x["category"] in ("relation-absent", "structural-honeypot"):
            x["structural"]["flagged"] = False
    v = gate(score(r))
    assert not v.passed and not v.insufficient
    assert any("MUTATION" in x for x in v.reasons)


def test_not_best_effort_fails():
    r = good_records()
    r[2]["labelledBestEffort"] = False
    v = gate(score(r))
    assert not v.passed and not v.insufficient
    assert any("best-effort" in x for x in v.reasons)


def test_never_block_fails():
    r = good_records()
    r[2]["blocked"] = True  # a block directive must never appear
    v = gate(score(r))
    assert not v.passed and not v.insufficient
    assert any("NEVER-BLOCK" in x for x in v.reasons)


def test_missing_category_insufficient():
    r = [x for x in good_records() if x["category"] != "future-certainty"]
    v = gate(score(r))
    assert not v.passed and v.insufficient


def test_live_frozen_corpus_passes():
    p = REPO / "corpus" / "validate-claim" / "verdicts.jsonl"
    if not p.exists():
        import pytest

        pytest.skip("frozen verdicts not present (run REGEN_VALIDATE_CLAIM_CORPUS=1 go test ...)")
    v = gate(score(load_records(p)))
    assert v.passed and not v.insufficient, v.reasons


# sanity: the helper does not accidentally mutate a shared dict across cases
def test_records_independent():
    a = good_records()
    b = copy.deepcopy(a)
    a[0]["full"]["flagged"] = True
    assert b[0]["full"]["flagged"] is False
