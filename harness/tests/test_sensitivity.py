"""Sensitivity-sweep machinery regression (doc 07 M6 / doc 11 M4).

Runs the sweep on the COMMITTED synthetic fixture against its committed ground
truth: at the pinned defaults precision and recall must both be 1.0 (the
fixture's trajectories are constructed, so the truth is exact), and a hostile
degraded-surfacing floor must cost recall WITHOUT costing precision — the exact
shape of evidence the calibrated default rests on.
"""

from __future__ import annotations

import json
import shutil
import subprocess
from pathlib import Path

import pytest

from harness.sensitivity import GridPoint, evaluate, render_report, sweep

REPO = Path(__file__).resolve().parents[2]
FIXTURE = REPO / "obsd" / "internal" / "replay" / "testdata" / "bundle-v1"
LABELS = REPO / "corpus" / "labels" / "bundle-v1.labels.json"
BINARY = REPO / "bin" / "replay"


def _replay_binary() -> Path:
    go = shutil.which("go")
    if BINARY.exists():
        return BINARY
    if go is None:
        pytest.skip("bin/replay not built and no Go toolchain on PATH (run `just build` first)")
    subprocess.run(
        [go, "build", "-o", str(BINARY), "./obsd/cmd/replay"],
        cwd=REPO, check=True, capture_output=True,
    )
    return BINARY


def _labels() -> dict:
    return json.loads(LABELS.read_text())


def test_pinned_defaults_score_perfectly() -> None:
    """The pinned regime (verification mode) hits every label with no FPs."""
    r = evaluate(_replay_binary(), FIXTURE, _labels(), GridPoint())
    assert r.precision == 1.0, f"false positives at the pinned defaults: {r.per_phenomenon}"
    assert r.recall == 1.0, f"missed labels at the pinned defaults: {r.per_phenomenon}"
    assert (r.cascade_hit, r.cascade_total, r.cascade_false) == (1, 1, 0), (
        "the leak->OOM cascade must be recognized exactly once-per-label with no false stories"
    )


def test_hostile_floor_costs_recall_not_precision() -> None:
    """A 0.9 completeness floor suppresses the (honest) degraded matches —
    recall drops, precision stays 1.0. This asymmetry IS the calibration
    argument for shipping min_completeness=0."""
    results = sweep(
        _replay_binary(), FIXTURE, _labels(),
        [GridPoint(), GridPoint(min_completeness=0.9)],
    )
    pinned, hostile = results
    assert pinned.recall == 1.0 and pinned.precision == 1.0
    assert hostile.recall < 1.0, "the floor must suppress degraded (sub-0.9) matches"
    assert hostile.precision == 1.0, "suppression must never fabricate"
    # The FULL throttling-cascade match survives any floor.
    assert hostile.per_phenomenon["PHEN_THROTTLING_CASCADE"].recall == 1.0


def test_band_widening_admits_premature_findings() -> None:
    """A wider at-threshold band (0.10) fires the leak BEFORE the truth window
    (110Mi against a 121.6Mi bar is 'at-threshold' under a 10% band) — measured
    as a precision loss against the tight ground truth, while recall holds.
    This asymmetry is the calibration evidence FOR the shipped band=0.05: the
    narrower band loses nothing on this corpus and admits no premature claims."""
    wide = evaluate(_replay_binary(), FIXTURE, _labels(), GridPoint(band=0.10))
    assert wide.recall == 1.0, "widening must never lose true positives"
    assert wide.precision < 1.0, (
        "the 0.10 band fires below the truth window on this corpus — if this "
        "stops happening the corpus changed and the band evidence must be re-derived"
    )
    base = evaluate(_replay_binary(), FIXTURE, _labels(), GridPoint())
    assert base.precision == 1.0 and base.recall == 1.0


def test_report_renders() -> None:
    results = sweep(_replay_binary(), FIXTURE, _labels(), [GridPoint()])
    md = render_report("bundle-v1", results)
    assert "| pinned | 1.000 | 1.000 |" in md
    assert "PHEN_MEMORY_LEAK" in md
