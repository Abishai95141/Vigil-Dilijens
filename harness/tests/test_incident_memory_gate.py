"""Regression + adversarial tests for the incident-memory gate (v3 T-B)."""

from __future__ import annotations

import copy
from pathlib import Path

from harness.incident_memory_gate import gate, incident_key, load_label, load_rows, score

REPO = Path(__file__).resolve().parents[2]
BUCKET = "2026-06-11T00:00:00Z"


def inc(phen: str, role: str, rec: int, unresolved: bool = False) -> dict:
    return {
        "key": incident_key(phen, role, BUCKET),
        "phenomenon": phen,
        "roleCei": role,
        "roleUnresolved": unresolved,
        "windowBucket": BUCKET,
        "recurrenceCount": rec,
    }


def good_bundles() -> dict:
    leak = inc("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", 2)
    recurring = [{"incidents": [leak]}]
    restart = [{"incidents": [copy.deepcopy(leak)]}]  # identical final = invariant
    continuous = [{"incidents": [inc("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", 1)]}]
    multi = [
        {
            "incidents": [
                inc("PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", 1),
                inc("PHEN_OOM_KILL_CGROUP", "r|c|ns|Deployment|payment", 1),
                inc("PHEN_OOM_KILL_SYSTEM", "i|c||Node|n1|uid", 1, unresolved=True),
            ]
        }
    ]
    return {
        "recurring": (
            recurring,
            {
                "bundle": "recurring",
                "scenario": "recurring",
                "expectDistinct": 1,
                "expectRecurrence": 2,
            },
        ),
        "restart": (
            restart,
            {
                "bundle": "restart",
                "scenario": "restart",
                "expectDistinct": 1,
                "expectRecurrence": 2,
            },
        ),
        "continuous": (
            continuous,
            {
                "bundle": "continuous",
                "scenario": "continuous",
                "expectDistinct": 1,
                "expectRecurrence": 1,
            },
        ),
        "multirole": (
            multi,
            {"bundle": "multirole", "scenario": "separation", "expectDistinct": 3},
        ),
    }


def test_good_corpus_passes():
    v = gate(score(good_bundles()))
    assert v.passed and not v.insufficient, v.reasons


def test_key_purity_violation_fails():
    b = good_bundles()
    b["recurring"][0][0]["incidents"][0]["key"] = "deadbeef"  # not reproducible
    v = gate(score(b))
    assert not v.passed and not v.insufficient
    assert any("key-purity" in r for r in v.reasons)


def test_grouping_violation_fails():
    b = good_bundles()
    b["recurring"][0][0]["incidents"][0]["recurrenceCount"] = 5  # != expected 2
    v = gate(score(b))
    assert not v.passed and not v.insufficient
    assert any("grouping" in r for r in v.reasons)


def test_continuous_inflation_fails():
    b = good_bundles()
    b["continuous"][0][0]["incidents"][0]["recurrenceCount"] = 4  # per-tick inflation
    v = gate(score(b))
    assert not v.passed and not v.insufficient
    assert any("continuous" in r or "grouping" in r for r in v.reasons)


def test_restart_invariance_break_fails():
    b = good_bundles()
    b["restart"][0][0]["incidents"][0]["recurrenceCount"] = 3  # diverges from recurring
    v = gate(score(b))
    assert not v.passed and not v.insufficient
    assert any("restart" in r or "grouping" in r for r in v.reasons)


def test_separation_break_fails():
    b = good_bundles()
    b["multirole"][0][0]["incidents"] = b["multirole"][0][0]["incidents"][:1]  # collapsed
    v = gate(score(b))
    assert not v.passed and not v.insufficient
    assert any("separation" in r or "grouping" in r for r in v.reasons)


def test_missing_scenario_insufficient():
    b = good_bundles()
    del b["continuous"]
    v = gate(score(b))
    assert not v.passed and v.insufficient


def test_live_frozen_corpus_passes():
    d = REPO / "corpus" / "incident-memory"
    names = ["recurring", "restart", "continuous", "multirole"]
    if not all((d / f"events-{n}.jsonl").exists() for n in names):
        import pytest

        pytest.skip("frozen incident corpus not present (run REGEN_INCIDENT_CORPUS=1 go test ...)")
    bundles = {}
    for n in names:
        label = load_label(d / f"label-{n}.json")
        bundles[label["bundle"]] = (load_rows(d / f"events-{n}.jsonl"), label)
    v = gate(score(bundles))
    assert v.passed and not v.insufficient, v.reasons
