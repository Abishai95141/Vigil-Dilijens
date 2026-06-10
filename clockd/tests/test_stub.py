"""Conformance + behaviour tests for the stub clock (doc 09 §2, §3.6)."""

from __future__ import annotations

import math

import pytest

from clockd import StubClock, stub_forecast


def test_point_holds_last_level_flat():
    series = [10.0, 11.0, 12.0, 13.0]
    r = stub_forecast(series, horizon=5, quantiles=[0.1, 0.5, 0.9])
    assert r.horizon == 5
    assert len(r.point) == 5
    # Honest zero-knowledge behaviour: hold the last level, do not invent a trend.
    assert all(p == 13.0 for p in r.point)


def test_band_never_collapses_and_widens_with_horizon():
    series = [float(x) for x in range(50)]  # steady slope -> nonzero sigma
    r = stub_forecast(series, horizon=8, quantiles=[0.1, 0.5, 0.9])
    lo = r.band(0)  # 0.1 quantile
    hi = r.band(2)  # 0.9 quantile
    widths = [hi[h] - lo[h] for h in range(r.horizon)]
    # The band has strictly positive width everywhere (never a line)...
    assert all(w > 0 for w in widths)
    # ...and widens monotonically with the horizon (uncertainty grows).
    assert all(widths[h + 1] > widths[h] for h in range(len(widths) - 1))


def test_flat_input_still_has_positive_band():
    series = [5.0] * 30  # zero volatility
    r = stub_forecast(series, horizon=4, quantiles=[0.05, 0.95])
    lo, hi = r.band(0), r.band(1)
    # Even with zero observed volatility the band must not collapse to a line.
    assert all(hi[h] - lo[h] > 0 for h in range(r.horizon))
    assert all(p == 5.0 for p in r.point)


def test_median_quantile_tracks_point():
    series = [1.0, 2.0, 1.5, 2.5, 2.0]
    r = stub_forecast(series, horizon=3, quantiles=[0.5])
    # The 0.5 quantile (z=0) coincides with the point trajectory.
    assert r.band(0) == pytest.approx(r.point)


def test_covariate_sharpens_additively():
    series = [100.0, 100.0, 100.0]
    cov = [1.0, 2.0, 3.0]
    r = stub_forecast(series, horizon=3, quantiles=[0.5], covariate_future=cov)
    assert r.point == pytest.approx([101.0, 102.0, 103.0])


def test_rejects_bad_inputs():
    with pytest.raises(ValueError):
        stub_forecast([], horizon=3, quantiles=[0.5])
    with pytest.raises(ValueError):
        stub_forecast([1.0], horizon=0, quantiles=[0.5])
    with pytest.raises(ValueError):
        stub_forecast([1.0], horizon=3, quantiles=[1.5])
    with pytest.raises(ValueError):
        stub_forecast([1.0], horizon=3, quantiles=[0.5], covariate_future=[1.0, 2.0])


def test_stubclock_is_swappable_and_ready():
    clock = StubClock()
    assert clock.ready() is True
    r = clock.forecast([1.0, 2.0, 3.0], horizon=2, quantiles=[0.1, 0.9])
    assert r.num_quantiles == 2
    assert len(r.quantile_values) == 2 * 2
    assert all(math.isfinite(v) for v in r.quantile_values)
