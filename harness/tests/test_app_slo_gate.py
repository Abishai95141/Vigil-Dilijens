"""Regression tests for the app-slo gate scorer (doc 15 cap. A).

The frozen corpus (L4 queue + L6 freshness + L1 load) must PASS; every floor must FAIL
on a targeted mutation — a scorer that cannot fail is not a gate.
"""

from __future__ import annotations

from pathlib import Path

from harness.app_slo_gate import gate, load_label, load_rows, score

CORPUS = Path(__file__).resolve().parents[2] / "corpus" / "app-slo"
SCENARIOS = [
    "over-slo",
    "under-slo",
    "undeclared-high-queue",
    "healthy-no-stream",
    "freshness-stale",
    "freshness-fresh",
    "freshness-undeclared",
    "load-over",
    "load-under",
    "load-undeclared",
]


def _bundles() -> dict:
    b = {}
    for s in SCENARIOS:
        label = load_label(CORPUS / f"label-{s}.json")
        b[label["bundle"]] = (load_rows(CORPUS / f"findings-{s}.jsonl"), label)
    return b


def _fire_row(phen: str = "PHEN_APP_QUEUE_SATURATION", metric: str = "app_queue_depth") -> dict:
    return {
        "Phenomenon": phen,
        "Quality": "full",
        "Members": [{"Metric": metric, "BarFlagged": False}],
    }


def test_frozen_corpus_passes():
    v = gate(score(_bundles()))
    assert v.passed, v.reasons
    assert not v.insufficient


def test_no_fabrication_fails_queue():
    b = _bundles()
    _, label = b["appslo-undeclared-high-queue"]
    # an undeclared SLO that suddenly produces a finding = a fabricated bar
    b["appslo-undeclared-high-queue"] = ([_fire_row()], label)
    v = gate(score(b))
    assert not v.passed
    assert any("NO-FABRICATION" in r for r in v.reasons)


def test_no_fabrication_fails_freshness():
    b = _bundles()
    _, label = b["appslo-freshness-undeclared"]
    bad = _fire_row("PHEN_APP_DATA_STALENESS", "app_last_update_seconds")
    b["appslo-freshness-undeclared"] = ([bad], label)
    v = gate(score(b))
    assert not v.passed
    assert any("NO-FABRICATION" in r for r in v.reasons)


def test_detection_fidelity_fails_on_missing_queue_fire():
    b = _bundles()
    _, label = b["appslo-over-slo"]
    b["appslo-over-slo"] = ([], label)  # expected to fire, but empty
    v = gate(score(b))
    assert not v.passed
    assert any("DETECTION-FIDELITY" in r for r in v.reasons)


def test_detection_fidelity_fails_on_missing_freshness_fire():
    b = _bundles()
    _, label = b["appslo-freshness-stale"]
    b["appslo-freshness-stale"] = ([], label)  # stale data must fire; empty is a miss
    v = gate(score(b))
    assert not v.passed
    assert any("DETECTION-FIDELITY" in r for r in v.reasons)


def test_borrowed_bar_fails_on_flagged_default():
    b = _bundles()
    _, label = b["appslo-load-over"]
    bad = _fire_row("PHEN_APP_LOAD_SURGE", "app_requests_total")
    bad["Members"][0]["BarFlagged"] = True  # a default bar, not the customer SLO
    b["appslo-load-over"] = ([bad], label)
    v = gate(score(b))
    assert not v.passed
    assert any("BORROWED-BAR" in r for r in v.reasons)


def test_cross_talk_fails():
    # The freshness scenario must light up ONLY PHEN_APP_DATA_STALENESS. Injecting a load
    # finding alongside it is cross-talk — the app phenomena are independent.
    b = _bundles()
    rows, label = b["appslo-freshness-stale"]
    intruder = _fire_row("PHEN_APP_LOAD_SURGE", "app_requests_total")
    b["appslo-freshness-stale"] = (rows + [intruder], label)
    v = gate(score(b))
    assert not v.passed
    assert any("NO-CROSS-TALK" in r for r in v.reasons)


def test_charter_fails():
    b = _bundles()
    _, label = b["appslo-over-slo"]
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
