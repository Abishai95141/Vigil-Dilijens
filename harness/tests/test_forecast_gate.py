"""Forecast gate scoring + verdict regressions (doc 11 M5 / 09 M3)."""

from __future__ import annotations

from datetime import UTC

from harness.forecast_gate import (
    MIN_SCORED_FORECASTS,
    gate,
    score_class,
)

NS = 1_000_000_000
CADENCE = 15 * NS
METRIC = "container_memory_working_set_bytes"
UID = "poduid-1/web"


def trace(basis_ns: int, point: list, lo: list, hi: list, bar: float, cand_steps: int | None,
          silence: str = "", latest_beyond: bool = False) -> dict:
    """One invocation trace; cand_steps = the candidate's projected crossing step index."""
    tr = {
        "entityCei": "i|cl|shop|Pod|web-a|poduid-1", "metric": METRIC, "streamUid": UID,
        "basisAt": iso(basis_ns), "contextPoints": 100,
        "barValue": bar, "direction": "above",
        "quantiles": [0.1, 0.5, 0.9], "point": point, "bands": [lo, point, hi],
    }
    if cand_steps is not None:
        tr["candidate"] = {
            "timeToCross": (cand_steps + 1) * CADENCE,
            "earliestAt": iso(basis_ns + 1 * CADENCE),
            "latestAt": iso(basis_ns + (len(point)) * CADENCE),
            "latestBeyondHorizon": latest_beyond,
        }
    else:
        tr["silence"] = silence or "no-crossing-within-horizon"
    return tr


def iso(ns: int) -> str:
    from datetime import datetime

    return datetime.fromtimestamp(ns / NS, tz=UTC).isoformat().replace("+00:00", "Z")


def readings_ramp(start_ns: int, n: int, start: float, step: float):
    return {(UID, METRIC): [(start_ns + i * CADENCE, start + step * i) for i in range(n)]}


def well_calibrated_events(n_forecasts: int) -> tuple[list, dict]:
    """Realized ramp 100 + 5/step; forecasts predict it with a ±20 band that the
    realized path ESCAPES at 2 of 16 steps (coverage 0.875 — realistically
    calibrated for a nominal-0.8 band; a 1.0-coverage band would itself fail
    the gate as over-covered, by design); bar crossed at step 8."""
    horizon = 16
    readings = readings_ramp(0, 200, 100.0, 5.0)
    events = []
    for k in range(n_forecasts):
        basis_idx = 20 + k  # forecast "as of" sample basis_idx
        basis_ns = basis_idx * CADENCE
        future_true = [100.0 + 5.0 * (basis_idx + 1 + h) for h in range(horizon)]
        bar = future_true[8]  # actual crossing at step 8
        lo = [v - 20 for v in future_true]
        hi = [v + 20 for v in future_true]
        for miss in (2, 12):  # realized falls below the lower edge here
            lo[miss] = future_true[miss] + 1
        events.append({
            "evalNow": iso(basis_ns), "barsEpoch": 1, "cadence": CADENCE,
            "traces": [trace(basis_ns, future_true, lo, hi, bar, cand_steps=8)],
            "silences": [], "unbudgeted": 0, "degraded": False,
        })
    return events, readings


def test_well_calibrated_class_passes():
    events, readings = well_calibrated_events(MIN_SCORED_FORECASTS + 2)
    rep = score_class(events, readings, METRIC)
    v = gate(rep)
    assert abs(rep.coverage - 0.875) < 1e-9
    assert rep.recall == 1.0
    assert rep.false_warnings == 0
    assert rep.crossings_in_band == rep.warned_crossings
    assert v.passed and not v.insufficient


def test_overconfident_bands_fail_coverage():
    events, readings = well_calibrated_events(MIN_SCORED_FORECASTS + 2)
    # Shrink every band to a sliver AROUND A WRONG LEVEL: realized escapes it.
    for e in events:
        tr = e["traces"][0]
        tr["bands"] = [
            [v + 100 for v in tr["point"]],
            tr["point"],
            [v + 101 for v in tr["point"]],
        ]
    rep = score_class(events, readings, METRIC)
    v = gate(rep)
    assert rep.coverage == 0.0
    assert not v.passed
    assert any("band coverage" in r for r in v.reasons)


def test_hidden_crossing_is_a_recall_miss():
    events, readings = well_calibrated_events(MIN_SCORED_FORECASTS + 2)
    for e in events:  # the pipeline stayed silent though reality crossed
        tr = e["traces"][0]
        del tr["candidate"]
        tr["silence"] = "flat-series"
    rep = score_class(events, readings, METRIC)
    v = gate(rep)
    assert rep.missed_crossings == rep.actual_crossings
    assert rep.misses_by_reason.get("flat-series") == rep.actual_crossings
    assert rep.events_warned == 0
    assert not v.passed
    assert any("event recall" in r for r in v.reasons)


def test_false_warning_counted_only_with_full_observation():
    events, readings = well_calibrated_events(MIN_SCORED_FORECASTS + 2)
    for e in events:  # bar far above anything realized: candidates are false
        e["traces"][0]["barValue"] = 1e9
    rep = score_class(events, readings, METRIC)
    assert rep.actual_crossings == 0
    assert rep.false_warnings == rep.candidates_full_obs > 0
    assert not gate(rep).passed


def test_thin_corpus_is_insufficient_never_pass():
    events, readings = well_calibrated_events(MIN_SCORED_FORECASTS - 1)
    v = gate(score_class(events, readings, METRIC))
    assert v.insufficient and not v.passed


def test_no_crossing_evidence_is_insufficient_even_with_good_coverage():
    """Corpus B1 lesson: coverage + silences alone must never ship a
    crossing-warning class."""
    events, readings = well_calibrated_events(MIN_SCORED_FORECASTS + 2)
    for e in events:  # nothing ever crosses, nothing ever warned
        tr = e["traces"][0]
        tr["barValue"] = 1e9
        del tr["candidate"]
        tr["silence"] = "no-crossing-within-horizon"
    v = gate(score_class(events, readings, METRIC))
    assert v.insufficient and not v.passed
    assert any("crossing evidence" in r for r in v.reasons)
