"""Clock conformance suite (doc 09 M1) — the swap test's Python half.

The SAME fixture cases run against every clock implementation. The contract
properties asserted here are the ones the charter and doc 09 §3.1/§3.6 demand:

  1. shapes: len(point) == horizon; len(quantile_values) == numQ * horizon
  2. per-step quantile monotonicity (q10 <= q50 <= q90 at every step)
  3. the band NEVER collapses to a line (q90 - q10 > 0 at every step)
  4. flat-honesty (flagged cases): a flat series forecasts flat — the point
     stays within 5% of the last observed level (no hallucinated trend)

Trend continuation on the ramp case is a MODEL-SKILL property, not a contract
property: the zero-knowledge StubClock is HONEST by going flat (doc 09 §2 —
"on truly random input it correctly goes flat"). Skill checks therefore run
only against the real model (skill=True), never in the swap contract.

StubClock runs always (pure stdlib). The TimesFM 2.5 adapter runs when
VIGIL_MODEL_CONFORMANCE=1 and the `model` extra is installed — heavy, opt-in,
and the probe-validated pins make it reproducible (doc 14 A15).
"""

from __future__ import annotations

import json
import os
import pathlib

import pytest

from clockd.forecast import ForecastResult, StubClock

CASES_PATH = pathlib.Path(__file__).parent / "conformance" / "cases.json"


def load_cases() -> dict:
    return json.loads(CASES_PATH.read_text())


def assert_contract(
    result: ForecastResult,
    horizon: int,
    quantiles: list[float],
    case: dict,
    skill: bool = False,
):
    name = case["name"]
    assert result.horizon == horizon, name
    assert result.num_quantiles == len(quantiles), name
    assert len(result.point) == horizon, name
    assert len(result.quantile_values) == len(quantiles) * horizon, name

    bands = [result.band(i) for i in range(len(quantiles))]
    lo, hi = bands[0], bands[-1]
    for h in range(horizon):
        for i in range(len(quantiles) - 1):
            assert bands[i][h] <= bands[i + 1][h] + 1e-9, f"{name}: quantile crossing at step {h}"
        assert hi[h] - lo[h] > 0, f"{name}: band collapsed to a line at step {h} (doc 01 §3)"

    checks = case.get("checks", {})
    if checks.get("flat_honesty"):
        last = case["series"][-1]
        for h in range(horizon):
            assert abs(result.point[h] - last) <= 0.05 * abs(last), (
                f"{name}: flat input grew a trend at step {h} ({result.point[h]} vs {last})"
            )
    if skill and checks.get("trend_up"):
        assert result.point[-1] > result.point[0], f"{name}: ramp projection flat-lined"


def run_suite(clock, skill: bool = False) -> None:
    doc = load_cases()
    horizon, quantiles = doc["horizon"], doc["quantiles"]
    for case in doc["cases"]:
        result = clock.forecast(case["series"], horizon, quantiles)
        assert_contract(result, horizon, quantiles, case, skill=skill)


def test_stub_clock_conformance():
    run_suite(StubClock())


@pytest.mark.skipif(
    os.environ.get("VIGIL_MODEL_CONFORMANCE") != "1",
    reason="model conformance is opt-in (VIGIL_MODEL_CONFORMANCE=1 + the `model` extra)",
)
def test_timesfm_clock_conformance():
    pytest.importorskip("timesfm")
    from clockd.timesfm_clock import TimesFMClock

    clock = TimesFMClock()
    assert clock.ready()
    run_suite(clock, skill=True)


@pytest.mark.skipif(
    os.environ.get("VIGIL_MODEL_CONFORMANCE") != "1",
    reason="model conformance is opt-in (VIGIL_MODEL_CONFORMANCE=1 + the `model` extra)",
)
def test_timesfm_refuses_tail_quantiles_and_covariates():
    """The adapter never fabricates: quantiles outside the decile head are
    refused (no tail extrapolation), covariates refused until Phase 4."""
    pytest.importorskip("timesfm")
    from clockd.timesfm_clock import TimesFMClock

    clock = TimesFMClock()
    series = load_cases()["cases"][0]["series"]
    with pytest.raises(ValueError):
        clock.forecast(series, 8, [0.05, 0.5, 0.95])
    with pytest.raises(NotImplementedError):
        clock.forecast(series, 8, [0.1, 0.5, 0.9], covariate_future=[0.0] * 8)
