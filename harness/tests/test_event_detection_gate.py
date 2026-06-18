"""Regression tests for the event-detection gate scorer (graph-robustness #2 G1).

The frozen corpus must PASS; every floor must FAIL on a targeted mutation. This is the
adversarial guard that keeps the gate honest — a scorer that cannot fail is not a gate.
"""

from __future__ import annotations

from pathlib import Path

from harness.event_detection_gate import gate, load_label, load_rows, score

CORPUS = Path(__file__).resolve().parents[2] / "corpus" / "event-detection"
SCENARIOS = [
    "oom-detection",
    "leak-to-oom-cascade",
    "throttle-to-probe-cascade",
    "image-pull-failure",
    "role-unresolved-no-upgrade",
    "unrelated-reason",
    "healthy-negative",
]


def _frozen_bundles() -> dict:
    bundles = {}
    for s in SCENARIOS:
        label = load_label(CORPUS / f"label-{s}.json")
        bundles[label["bundle"]] = (
            load_rows(CORPUS / f"findings-{s}.jsonl"),
            load_rows(CORPUS / f"cascades-{s}.jsonl"),
            label,
        )
    return bundles


def test_frozen_corpus_passes():
    g = score(_frozen_bundles())
    v = gate(g)
    assert v.passed, v.reasons
    assert not v.insufficient
    # the two flagship cascades are present
    assert g.cascades_recognized == 2


def test_false_upgrade_fails():
    b = _frozen_bundles()
    # inject a phenomenon finding the oracle never expected (role-less event upgraded)
    rows, csc, label = b["eventdetect-role-unresolved-no-upgrade"]
    rows = rows + [
        {
            "Phenomenon": "PHEN_OOM_KILL_CGROUP",
            "EntityCEI": "i|x|Node|worker",
            "Quality": "degraded",
            "RequiredMet": 1,
            "RequiredTotal": 8,
            "Members": [{"Metric": "k8s-event:OOMKilled"}],
            "Unobservable": ["a"] * 7,
        }
    ]
    b["eventdetect-role-unresolved-no-upgrade"] = (rows, csc, label)
    v = gate(score(b))
    assert not v.passed
    assert any("FALSE-UPGRADE" in r for r in v.reasons)


def test_missing_cascade_fails():
    b = _frozen_bundles()
    # drop the recognized leak->OOM cascade while the oracle still expects it
    rows, _, label = b["eventdetect-leak-to-oom-cascade"]
    b["eventdetect-leak-to-oom-cascade"] = (rows, [], label)
    v = gate(score(b))
    assert not v.passed
    assert any("CASCADE-RECOGNITION" in r for r in v.reasons)


def test_phantom_cascade_fails():
    b = _frozen_bundles()
    rows, csc, label = b["eventdetect-oom-detection"]
    phantom = [
        {
            "Trigger": {"Phenomenon": "PHEN_MEMORY_LEAK"},
            "Downstream": {"Phenomenon": "PHEN_OOM_KILL_CGROUP"},
            "Why": "Eventual outcome",
            "Related": "same-entity",
        }
    ]
    b["eventdetect-oom-detection"] = (rows, phantom, label)
    v = gate(score(b))
    assert not v.passed
    assert any("phantom" in r.lower() for r in v.reasons)


def test_digest_drift_fails():
    b = _frozen_bundles()
    rows, csc, label = b["eventdetect-leak-to-oom-cascade"]
    label = dict(label, digestAfter="deadbeef")
    b["eventdetect-leak-to-oom-cascade"] = (rows, csc, label)
    v = gate(score(b))
    assert not v.passed
    assert any("DIGEST-INVARIANCE" in r for r in v.reasons)


def test_degraded_honesty_fails():
    b = _frozen_bundles()
    rows, csc, label = b["eventdetect-oom-detection"]
    # claim a FULL match (the discrete event is never a full match)
    rows = [dict(r, Quality="full", RequiredMet=8) for r in rows]
    b["eventdetect-oom-detection"] = (rows, csc, label)
    v = gate(score(b))
    assert not v.passed
    assert any("DEGRADED-HONEST" in r or "DETECTION-FIDELITY" in r for r in v.reasons)


def test_charter_fails():
    b = _frozen_bundles()
    rows, csc, label = b["eventdetect-oom-detection"]
    rows = [dict(r, Note="the leak caused the oom") for r in rows]
    b["eventdetect-oom-detection"] = (rows, csc, label)
    v = gate(score(b))
    assert not v.passed
    assert any("charter" in r.lower() for r in v.reasons)


def test_missing_scenario_is_insufficient():
    b = _frozen_bundles()
    del b["eventdetect-throttle-to-probe-cascade"]
    v = gate(score(b))
    assert not v.passed
    assert v.insufficient
