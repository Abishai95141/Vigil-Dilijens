"""Regression tests for the forecast decision-layer FP gate (near-miss corpus).

The frozen corpus must PASS; every floor must FAIL on a targeted mutation. The CARDINAL
FP-on-near-miss floor (a recede/plateau/seductive series must never warn) gets the most
attention — it is the precision boundary the live corpus could never exercise.
"""

from __future__ import annotations

from pathlib import Path

from harness.forecast_fp_gate import gate, load_json, score

CORPUS = Path(__file__).resolve().parents[2] / "corpus" / "forecast-nearmiss"
SCENARIOS = [
    "recede-above",
    "plateau-below",
    "seductive-wide-upper",
    "band-too-wide",
    "imminent-crossing-above",
    "open-tail-crossing",
    "recede-below",
    "crossing-below",
    "nan-point",
    "dip-then-cross",
]


def _bundles() -> dict:
    b = {}
    for s in SCENARIOS:
        label = load_json(CORPUS / f"label-{s}.json")
        b[label["bundle"]] = (load_json(CORPUS / f"result-{s}.json"), label)
    return b


def test_frozen_corpus_passes():
    v = gate(score(_bundles()))
    assert v.passed, v.reasons
    assert not v.insufficient


def test_fp_on_nearmiss_fails():
    # CARDINAL: flip a recede scenario to "emitted" — the gate must catch the false warning.
    b = _bundles()
    key = "forecast-nearmiss-recede-above"
    result, label = b[key]
    cand = {"class": "PROJECTED", "isProjection": True}
    b[key] = ({**result, "emitted": True, "silence": "", "candidate": cand}, label)
    v = gate(score(b))
    assert not v.passed
    assert any("FP-ON-NEARMISS" in r for r in v.reasons), v.reasons


def test_wrong_silence_reason_fails():
    # A regression that silences for the WRONG reason (no-crossing where band-too-wide is
    # the real boundary, or vice versa) must be caught — not just "candidate is nil".
    b = _bundles()
    key = "forecast-nearmiss-band-too-wide"
    result, label = b[key]
    b[key] = ({**result, "silence": "no-crossing-within-horizon"}, label)
    v = gate(score(b))
    assert not v.passed
    assert any("SILENCE-REASON" in r for r in v.reasons), v.reasons


def test_recall_miss_fails():
    # A genuine-crossing control losing its warning is a recall regression.
    b = _bundles()
    key = "forecast-nearmiss-imminent-crossing-above"
    result, label = b[key]
    silenced = dict(result)
    silenced.update(emitted=False, silence="no-crossing-within-horizon", candidate=None)
    b[key] = (silenced, label)
    v = gate(score(b))
    assert not v.passed
    assert any("RECALL" in r for r in v.reasons), v.reasons


def test_params_drift_fails():
    # The frozen verdict is only meaningful at the pinned params; a drift must fail.
    b = _bundles()
    key = "forecast-nearmiss-recede-above"
    result, label = b[key]
    b[key] = (result, {**label, "params": {**label["params"], "maxBandRatio": 0.9}})
    v = gate(score(b))
    assert not v.passed
    assert any("PARAMS" in r for r in v.reasons), v.reasons


def test_charter_violation_fails():
    # A causal/future-certainty phrase smuggled into an emitted candidate must be caught.
    b = _bundles()
    key = "forecast-nearmiss-imminent-crossing-above"
    result, label = b[key]
    cand = dict(result["candidate"])
    cand["note"] = "this will cross because of the leak"
    b[key] = ({**result, "candidate": cand}, label)
    v = gate(score(b))
    assert not v.passed
    assert any("charter" in r for r in v.reasons), v.reasons


def test_missing_required_scenario_insufficient():
    # Dropping a required scenario is INSUFFICIENT (never a silent pass).
    b = _bundles()
    del b["forecast-nearmiss-seductive-wide-upper"]
    v = gate(score(b))
    assert not v.passed
    assert v.insufficient
    assert any("seductive-wide-upper" in r for r in v.reasons), v.reasons
