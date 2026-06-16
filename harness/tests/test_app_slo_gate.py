"""Regression tests for the app-slo gate scorer (doc 15 cap. A).

The frozen corpus must PASS; every floor must FAIL on a targeted mutation — a scorer
that cannot fail is not a gate.
"""

from __future__ import annotations

from pathlib import Path

from harness.app_slo_gate import gate, load_label, load_rows, score

CORPUS = Path(__file__).resolve().parents[2] / "corpus" / "app-slo"
SCENARIOS = ["over-slo", "under-slo", "undeclared-high-queue", "healthy-no-stream"]


def _bundles() -> dict:
    b = {}
    for s in SCENARIOS:
        label = load_label(CORPUS / f"label-{s}.json")
        b[label["bundle"]] = (load_rows(CORPUS / f"findings-{s}.jsonl"), label)
    return b


def test_frozen_corpus_passes():
    v = gate(score(_bundles()))
    assert v.passed, v.reasons
    assert not v.insufficient


def _fire_row() -> dict:
    return {
        "Phenomenon": "PHEN_APP_QUEUE_SATURATION",
        "Quality": "full",
        "Members": [{"Metric": "app_queue_depth", "BarFlagged": False}],
    }


def test_no_fabrication_fails():
    b = _bundles()
    rows, label = b["appslo-undeclared-high-queue"]
    # an undeclared SLO that suddenly produces a finding = a fabricated bar
    b["appslo-undeclared-high-queue"] = ([_fire_row()], label)
    v = gate(score(b))
    assert not v.passed
    assert any("NO-FABRICATION" in r for r in v.reasons)


def test_detection_fidelity_fails_on_missing_fire():
    b = _bundles()
    rows, label = b["appslo-over-slo"]
    b["appslo-over-slo"] = ([], label)  # expected to fire, but empty
    v = gate(score(b))
    assert not v.passed
    assert any("DETECTION-FIDELITY" in r for r in v.reasons)


def test_borrowed_bar_fails_on_flagged_default():
    b = _bundles()
    rows, label = b["appslo-over-slo"]
    bad = _fire_row()
    bad["Members"][0]["BarFlagged"] = True  # a default bar, not the customer SLO
    b["appslo-over-slo"] = ([bad], label)
    v = gate(score(b))
    assert not v.passed
    assert any("BORROWED-BAR" in r for r in v.reasons)


def test_charter_fails():
    b = _bundles()
    rows, label = b["appslo-over-slo"]
    bad = _fire_row()
    bad["Note"] = "the load caused the backlog"
    b["appslo-over-slo"] = ([bad], label)
    v = gate(score(b))
    assert not v.passed
    assert any("charter" in r.lower() for r in v.reasons)


def test_missing_scenario_is_insufficient():
    b = _bundles()
    del b["appslo-healthy-no-stream"]
    v = gate(score(b))
    assert not v.passed
    assert v.insufficient
