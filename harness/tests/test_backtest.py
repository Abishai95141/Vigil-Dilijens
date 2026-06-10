"""Tests for the forecast backtest primitives (doc 11 §3.5)."""

from __future__ import annotations

import numpy as np
import pytest

from harness import band_coverage, time_to_cross_error


def test_full_coverage():
    realized = [1.0, 2.0, 3.0]
    lower = [0.0, 1.0, 2.0]
    upper = [2.0, 3.0, 4.0]
    assert band_coverage(realized, lower, upper) == 1.0


def test_partial_coverage():
    realized = [1.0, 5.0, 3.0, 9.0]  # 5.0 and 9.0 fall outside
    lower = [0.0, 0.0, 2.0, 0.0]
    upper = [2.0, 2.0, 4.0, 2.0]
    assert band_coverage(realized, lower, upper) == 0.5


def test_inclusive_bounds():
    # Values exactly on the bound count as covered.
    assert band_coverage([1.0, 2.0], [1.0, 0.0], [3.0, 2.0]) == 1.0


def test_well_calibrated_band_recovers_nominal_level():
    rng = np.random.default_rng(42)
    n = 20000
    realized = rng.normal(0.0, 1.0, size=n)
    # A correct 10-90% band for a standard normal is roughly [-1.2816, 1.2816].
    q = 1.2815515594
    lower = np.full(n, -q)
    upper = np.full(n, q)
    cov = band_coverage(realized, lower, upper)
    assert cov == pytest.approx(0.80, abs=0.02)


def test_shape_and_empty_validation():
    with pytest.raises(ValueError):
        band_coverage([1.0, 2.0], [0.0], [3.0, 4.0])
    with pytest.raises(ValueError):
        band_coverage([], [], [])
    with pytest.raises(ValueError):
        band_coverage([1.0], [2.0], [1.0])  # upper < lower


def test_time_to_cross_error_sign():
    assert time_to_cross_error(10.0, 8.0) == 2.0  # late
    assert time_to_cross_error(8.0, 10.0) == -2.0  # early
