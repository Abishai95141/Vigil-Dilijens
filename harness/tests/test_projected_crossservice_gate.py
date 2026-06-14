"""Anticipatory cross-service gate scoring + verdict regressions (doc 15 E / 11 §3.5)."""

from __future__ import annotations

from pathlib import Path

from harness.projected_crossservice_gate import (
    LEAD_TIME_MIN_SECONDS,
    MIN_CONFIRMED_ROOTS,
    GateReport,
    gate,
    load_events,
    load_label,
    score_bundle,
)

CORPUS = Path(__file__).resolve().parents[2] / "corpus" / "crossservice-projected"


def ptick(ts: str, proj: tuple | None = None, meas: tuple | None = None,
          charter: bool = True, band_open: bool = False) -> dict:
    """One per-tick record. proj/meas = (root, [impacted]) or None."""
    return {
        "evalNow": ts, "barsEpoch": 1,
        "warned": [proj[0]] if proj else [],
        "projFired": proj is not None,
        "projRoot": proj[0] if proj else "",
        "projImpacted": proj[1] if proj else [],
        "projCrossAt": ts, "projEarliest": ts, "projLatest": ts, "projBandOpen": band_open,
        "measFired": meas is not None,
        "measRoot": meas[0] if meas else "",
        "measImpacted": meas[1] if meas else [],
        "charterClean": charter,
    }


def ts(minute: int, second: int = 0) -> str:
    return f"2026-06-14T13:{minute:02d}:{second:02d}Z"


# Two independent confirmed scenarios + their callers.
R1, C1 = "chaos/forecast-callee", ["chaos/forecast-caller"]
R2, C2 = "chaos/forecast2-callee", ["chaos/forecast2-caller"]


def positive_bundle(name: str, root: str, callers: list[str],
                    proj_start: int, meas_start: int,
                    n_proj: int = 8, n_meas: int = 4) -> tuple[list[dict], dict]:
    """Anticipatory ticks from proj_start; measured ticks from meas_start (> proj_start).
    The forecast keeps warning through the crossing (proj + meas overlap)."""
    events: list[dict] = []
    for i in range(n_proj):
        m = proj_start + i
        meas = (root, callers) if m >= meas_start else None
        events.append(ptick(ts(m), proj=(root, callers), meas=meas))
    # A few measured-only ticks after the forecast stops re-warning.
    for i in range(n_meas):
        events.append(ptick(ts(proj_start + n_proj + i), meas=(root, callers)))
    label = {"bundle": name, "scenario": "positive", "expect_proj_fire": True,
             "roots": [root], "callers": {root: callers}}
    return events, label


def negative_bundle(name: str, n_ticks: int, measured_root: str | None = None,
                    callers: list[str] | None = None) -> tuple[list[dict], dict]:
    """A scenario the forecast does NOT anticipate. Optionally an abrupt MEASURED
    cascade (measured fires, but no anticipatory chain ever does)."""
    events = []
    for i in range(n_ticks):
        meas = (measured_root, callers or []) if measured_root else None
        events.append(ptick(ts(i), proj=None, meas=meas))
    label = {"bundle": name, "scenario": "negative", "expect_proj_fire": False}
    return events, label


def pos(name, root, callers, proj_start=0, meas_start=12, **kw):
    return score_bundle(*positive_bundle(name, root, callers, proj_start, meas_start, **kw))


def neg(name, n, measured_root=None, callers=None):
    return score_bundle(*negative_bundle(name, n, measured_root, callers))


def passing_corpus() -> GateReport:
    """Two independent confirmed roots (lead 12min and 10min) + an abrupt measured-only
    negative that the forecast never anticipates — the shape a real PASS has."""
    return GateReport(bundles=[
        pos("fcast-1", R1, C1, proj_start=0, meas_start=12),
        pos("fcast-2", R2, C2, proj_start=0, meas_start=10),
        neg("abrupt-fault", 24, measured_root="ob/productcatalogservice",
            callers=["ob/frontend"]),
    ])


def test_clean_corpus_passes():
    g = passing_corpus()
    v = gate(g)
    assert len(g.confirmed_roots) == 2
    assert g.worst_lead_seconds >= LEAD_TIME_MIN_SECONDS
    assert g.false_anticipations == 0
    assert g.charter_violations == 0
    assert not g.fidelity_mismatches
    assert v.passed and not v.insufficient


def test_no_lead_does_not_confirm():
    """A projection that fires the SAME tick as the measured reality led nothing →
    that root is not confirmed (and if labeled, the gate fails on it)."""
    ev, lbl = positive_bundle("sametick", R1, C1, proj_start=0, meas_start=0)
    g = GateReport(bundles=[
        score_bundle(ev, lbl),
        pos("fcast-2", R2, C2, proj_start=0, meas_start=10),
        neg("abrupt-fault", 24),
    ])
    v = gate(g)
    assert R1 not in g.confirmed_roots
    assert not v.passed and not v.insufficient
    assert any("did not confirm" in r for r in v.reasons)


def test_sub_floor_lead_fails():
    """A confirmed root whose lead is below the operator-value floor fails."""
    # proj at 13:00:00, meas at 13:01:00 → lead 60s < 120s floor.
    ev = [ptick(ts(0, 0), proj=(R1, C1))]
    ev += [ptick(ts(0, 30), proj=(R1, C1))]
    ev += [ptick(ts(1, 0), proj=(R1, C1), meas=(R1, C1))]
    ev += [ptick(ts(1, 15), proj=(R1, C1), meas=(R1, C1))]
    ev += [ptick(ts(1, 30), proj=(R1, C1), meas=(R1, C1))]
    lbl = {"bundle": "shortlead", "scenario": "positive", "expect_proj_fire": True,
           "roots": [R1], "callers": {R1: C1}}
    g = GateReport(bundles=[
        score_bundle(ev, lbl),
        pos("fcast-2", R2, C2, proj_start=0, meas_start=10),
        neg("abrupt-fault", 24),
    ])
    v = gate(g)
    assert R1 in g.confirmed_roots
    assert not v.passed and not v.insufficient
    assert any("lead" in r for r in v.reasons)


def test_false_anticipation_fails():
    """An anticipatory chain in a negative scenario = the forecast fabricated a crossing."""
    ev, lbl = negative_bundle("abrupt-fault", 24, measured_root="ob/x", callers=["ob/y"])
    ev[5] = ptick(ts(5), proj=("ob/x", ["ob/y"]))  # an anticipatory fire where none was due
    g = GateReport(bundles=[
        pos("fcast-1", R1, C1, proj_start=0, meas_start=12),
        pos("fcast-2", R2, C2, proj_start=0, meas_start=10),
        score_bundle(ev, lbl),
    ])
    v = gate(g)
    assert g.false_anticipations == 1
    assert not v.passed and not v.insufficient
    assert any("false anticipation" in r for r in v.reasons)


def test_charter_violation_fails():
    ev, lbl = positive_bundle("fcast-1", R1, C1, proj_start=0, meas_start=12)
    ev[2]["charterClean"] = False  # a fused causal token / collapsed band
    g = GateReport(bundles=[
        score_bundle(ev, lbl),
        pos("fcast-2", R2, C2, proj_start=0, meas_start=10),
        neg("abrupt-fault", 24),
    ])
    v = gate(g)
    assert g.charter_violations == 1
    assert not v.passed and not v.insufficient
    assert any("charter" in r for r in v.reasons)


def test_fidelity_mismatch_fails():
    """A confirmed root whose PROJECTED fan-in differs from its MEASURED fan-in (a
    phantom or missed projected caller) breaks structural fidelity."""
    # Projected impact names an extra phantom caller the measured walk never did.
    ev = []
    for i in range(8):
        meas = (R1, C1) if i >= 4 else None
        ev.append(ptick(ts(i), proj=(R1, ["chaos/forecast-caller", "chaos/phantom"]), meas=meas))
    lbl = {"bundle": "phantom", "scenario": "positive", "expect_proj_fire": True,
           "roots": [R1], "callers": {R1: C1}}
    g = GateReport(bundles=[
        score_bundle(ev, lbl),
        pos("fcast-2", R2, C2, proj_start=0, meas_start=4),
        neg("abrupt-fault", 24),
    ])
    v = gate(g)
    assert g.fidelity_mismatches
    assert not v.passed and not v.insufficient
    assert any("fidelity" in r for r in v.reasons)


def test_open_projection_unmet_labeled_root_fails():
    """A labeled root that anticipates but NEVER materialises did not confirm."""
    ev = [ptick(ts(i), proj=(R1, C1)) for i in range(8)]  # forecast warns, crossing never comes
    lbl = {"bundle": "open", "scenario": "positive", "expect_proj_fire": True,
           "roots": [R1], "callers": {R1: C1}}
    g = GateReport(bundles=[
        score_bundle(ev, lbl),
        pos("fcast-2", R2, C2, proj_start=0, meas_start=10),
        neg("abrupt-fault", 24),
    ])
    v = gate(g)
    assert R1 not in g.confirmed_roots
    assert not v.passed and not v.insufficient
    assert any("did not confirm" in r for r in v.reasons)


def test_one_confirmed_root_is_insufficient():
    """A single confirmed ramp cannot carry the gate, however clean its lead."""
    g = GateReport(bundles=[
        pos("fcast-1", R1, C1, proj_start=0, meas_start=12),
        neg("abrupt-fault", 24),
    ])
    v = gate(g)
    assert v.insufficient and not v.passed
    assert any("confirmed diversity" in r for r in v.reasons)


def test_thin_anticipatory_corpus_is_insufficient():
    g = GateReport(bundles=[
        pos("fcast-1", R1, C1, proj_start=0, meas_start=12, n_proj=2, n_meas=1),
        neg("abrupt-fault", 24),
    ])
    # n_proj is tiny across positives → below MIN_PROJ_TICKS.
    v = gate(g)
    assert v.insufficient and not v.passed


def test_no_quiet_evidence_is_insufficient():
    """Two CONFIRMED positives but NO negative scenario (where the forecast should stay
    silent): the no-false-anticipation claim is unsubstantiated → INSUFFICIENT."""
    g = GateReport(bundles=[
        pos("fcast-1", R1, C1, proj_start=0, meas_start=4),
        pos("fcast-2", R2, C2, proj_start=0, meas_start=4),
    ])
    v = gate(g)
    assert len(g.confirmed_roots) == 2  # both confirmed; the gap is purely the missing negative
    assert v.insufficient and not v.passed
    assert any("quiet evidence" in r for r in v.reasons)


def test_stray_anticipation_in_positive_fails():
    """An anticipatory chain on an UNDECLARED root inside a positive scenario (the
    forecast warned a service the corpus did not expect to lead) is a failure."""
    ev, lbl = positive_bundle("fcast-1", R1, C1, proj_start=0, meas_start=4)
    ev.append(ptick(ts(20), proj=("chaos/unexpected-svc", ["chaos/x"])))  # undeclared anticipation
    g = GateReport(bundles=[
        score_bundle(ev, lbl),
        pos("fcast-2", R2, C2, proj_start=0, meas_start=4),
        neg("abrupt-fault", 24),
    ])
    v = gate(g)
    assert g.stray_anticipations == 1
    assert not v.passed and not v.insufficient
    assert any("stray" in r for r in v.reasons)


def test_measured_before_projection_not_confirmed():
    """A measured fire that PRECEDES the projection means the reality was already there,
    not anticipated — that root must NOT confirm (the lead-inflation guard)."""
    # measured at 13:00, projection at 13:05, measured again at 13:10 — must NOT credit
    # a 5-min lead against the later measured fire.
    ev = [ptick(ts(0), meas=(R1, C1))]            # measured BEFORE any projection
    ev += [ptick(ts(m), proj=(R1, C1)) for m in range(5, 9)]
    ev += [ptick(ts(10), proj=(R1, C1), meas=(R1, C1))]
    lbl = {"bundle": "stale", "scenario": "positive", "expect_proj_fire": True,
           "roots": [R1], "callers": {R1: C1}}
    g = GateReport(bundles=[
        score_bundle(ev, lbl),
        pos("fcast-2", R2, C2, proj_start=0, meas_start=4),
        neg("abrupt-fault", 24),
    ])
    v = gate(g)
    assert R1 not in g.confirmed_roots  # measured predated the projection → not anticipated
    assert not v.passed and not v.insufficient
    assert any("did not confirm" in r for r in v.reasons)


def test_live_captured_corpus_passes():
    """Regression on the FROZEN, live-captured corpus (corpus/crossservice-projected/)
    — the exact `replay -projected-crossservice` output that passed the gate on the
    kind cluster: two independent forecast-led leaks (each anticipated minutes before
    its measured cross-service cascade) + a negative the forecast never anticipates.
    If the scorer or the corpus ever regresses, this fails."""
    if not (CORPUS / "events-pos1.jsonl").exists():
        import pytest
        pytest.skip("live projected corpus events not yet frozen")
    scenarios = [("events-pos1.jsonl", "label-pos1.json"),
                 ("events-pos2.jsonl", "label-pos2.json"),
                 ("events-negative.jsonl", "label-negative.json")]
    bundles = [score_bundle(load_events(CORPUS / ev), load_label(CORPUS / lbl))
               for ev, lbl in scenarios]
    g = GateReport(bundles=bundles)
    v = gate(g)
    assert len(g.confirmed_roots) >= MIN_CONFIRMED_ROOTS
    assert g.worst_lead_seconds >= LEAD_TIME_MIN_SECONDS
    assert g.false_anticipations == 0
    assert g.charter_violations == 0
    assert v.passed and not v.insufficient
