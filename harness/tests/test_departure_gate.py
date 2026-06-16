"""Regression tests for the departure gate scorer (doc 15 cap. C).

The frozen corpus must PASS; every floor must FAIL on a targeted mutation. The CARDINAL
FP-on-decoys floor (a noisy-but-stationary series must never false-fire) gets the most
attention.
"""

from __future__ import annotations

from pathlib import Path

from harness.departure_gate import gate, load_label, load_rows, score

CORPUS = Path(__file__).resolve().parents[2] / "corpus" / "departure"
SCENARIOS = [
    "step-above", "step-below", "noisy-decoy", "wide-band-absorbs",
    "near-miss-margin", "healthy-inside", "edge-inside", "zero-width-band",
]


def _bundles() -> dict:
    b = {}
    for s in SCENARIOS:
        label = load_label(CORPUS / f"label-{s}.json")
        b[label["bundle"]] = (load_rows(CORPUS / f"departures-{s}.jsonl"), label)
    return b


def _dep(side: str = "above") -> dict:
    return {
        "entityCei": "i|x", "metric": "working_set", "side": side,
        "class": "PROJECTED band ⋈ MEASURED sample (joined, never fused)",
        "realized": 50, "lower": 10, "upper": 20, "detail": "left its projected band",
    }


def test_frozen_corpus_passes():
    v = gate(score(_bundles()))
    assert v.passed, v.reasons
    assert not v.insufficient


def test_fp_on_decoy_fails():
    # Forge a departure onto a decoy (noisy-but-stationary) — the cardinal anti-FP violation.
    b = _bundles()
    _, label = b["departure-noisy-decoy"]
    b["departure-noisy-decoy"] = ([_dep()], label)
    v = gate(score(b))
    assert not v.passed
    assert any("FP-ON-DECOYS" in r for r in v.reasons)


def test_recall_miss_fails():
    b = _bundles()
    _, label = b["departure-step-above"]
    b["departure-step-above"] = ([], label)  # a true step that produced nothing
    v = gate(score(b))
    assert not v.passed
    assert any("RECALL" in r for r in v.reasons)


def test_side_fidelity_fails():
    b = _bundles()
    _, label = b["departure-step-above"]
    b["departure-step-above"] = ([_dep(side="below")], label)  # wrong side
    v = gate(score(b))
    assert not v.passed
    assert any("SIDE-FIDELITY" in r for r in v.reasons)


def test_projected_class_fails():
    b = _bundles()
    _, label = b["departure-step-above"]
    bad = _dep()
    bad["class"] = "MEASURED anomaly"  # class laundering
    b["departure-step-above"] = ([bad], label)
    v = gate(score(b))
    assert not v.passed
    assert any("PROJECTED-CLASS" in r for r in v.reasons)


def test_charter_fails():
    b = _bundles()
    _, label = b["departure-step-above"]
    bad = _dep()
    bad["detail"] = "the spike was caused by the upstream"
    b["departure-step-above"] = ([bad], label)
    v = gate(score(b))
    assert not v.passed
    assert any("charter" in r.lower() for r in v.reasons)


def test_missing_scenario_is_insufficient():
    b = _bundles()
    del b["departure-edge-inside"]
    v = gate(score(b))
    assert not v.passed
    assert v.insufficient
