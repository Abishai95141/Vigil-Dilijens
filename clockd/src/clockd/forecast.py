"""The stub clock — an honest, pure-stdlib forecaster conforming to the clock
contract (doc 09 §3.1).

Honest behaviour (doc 09 §2): on random/flat input the point forecast goes flat
(it holds the last level — it does not hallucinate a trend), and the uncertainty
band NEVER collapses to a line (doc 01 §3, doc 09 §3.6) — it widens with the
horizon. This is a placeholder for TimesFM 2.5, deliberately marked as a stub. Its
job is to prove the contract and the swap path, not to be accurate.

Charter note: the input is a bare float sequence. There are no string parameters
anywhere in this module — mirroring the no-string-fields rule the clock proto
enforces at the wire level.
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from statistics import NormalDist


@dataclass(frozen=True)
class ForecastResult:
    """Output of a forecast: a point trajectory plus a quantile band.

    quantile_values is flattened row-major as [num_quantiles][horizon] to mirror
    the wire shape in clock.proto (ForecastResponse).
    """

    point: list[float]
    quantile_values: list[float]
    num_quantiles: int
    horizon: int

    def band(self, q_index: int) -> list[float]:
        """Return the horizon-length slice for the q_index-th quantile."""
        start = q_index * self.horizon
        return self.quantile_values[start : start + self.horizon]


# Smallest band half-width per step, so even a perfectly flat series yields a band
# with positive width (the line-never-collapses guarantee), not a degenerate point.
_MIN_SIGMA = 1e-9


def _recent_sigma(series: list[float], lookback: int = 64) -> float:
    """Estimate step-to-step volatility from recent first differences."""
    tail = series[-(lookback + 1) :]
    diffs = [tail[i + 1] - tail[i] for i in range(len(tail) - 1)]
    if not diffs:
        return _MIN_SIGMA
    mean = sum(diffs) / len(diffs)
    var = sum((d - mean) ** 2 for d in diffs) / len(diffs)
    return max(math.sqrt(var), _MIN_SIGMA)


def stub_forecast(
    series: list[float],
    horizon: int,
    quantiles: list[float],
    covariate_future: list[float] | None = None,
) -> ForecastResult:
    """Forecast `horizon` steps from `series`, with a quantile band per level.

    - point: holds the last observed level (flat) — honest for a zero-knowledge stub.
    - band: last level ± z(q) · sigma · sqrt(step), a random-walk-style widening band
      that never collapses to a line.
    - covariate_future: optional known-future regressor (doc 09 §3.5). It SHARPENS
      the trajectory additively; it is never surfaced as an explanation.
    """
    if horizon <= 0:
        raise ValueError(f"horizon must be > 0, got {horizon}")
    if not series:
        raise ValueError("series must be non-empty")
    if covariate_future is not None and len(covariate_future) not in (0, horizon):
        raise ValueError(
            f"covariate_future, when present, must have length == horizon ({horizon}), "
            f"got {len(covariate_future)}"
        )

    last = float(series[-1])
    sigma = _recent_sigma(series)

    point = [last] * horizon
    if covariate_future:
        point = [point[h] + float(covariate_future[h]) for h in range(horizon)]

    quantile_values: list[float] = []
    for q in quantiles:
        if not (0.0 < q < 1.0):
            raise ValueError(f"quantile levels must be in (0,1), got {q}")
        z = NormalDist().inv_cdf(q)
        for h in range(horizon):
            spread = z * sigma * math.sqrt(h + 1)
            quantile_values.append(point[h] + spread)

    return ForecastResult(
        point=point,
        quantile_values=quantile_values,
        num_quantiles=len(quantiles),
        horizon=horizon,
    )


class StubClock:
    """A clock implementation backed by stub_forecast.

    Satisfies the swap contract (doc 09 §3.1): any conformant model is a drop-in.
    The real TimesFM-backed clock will implement the same `forecast` signature.
    """

    def forecast(
        self,
        series: list[float],
        horizon: int,
        quantiles: list[float],
        covariate_future: list[float] | None = None,
    ) -> ForecastResult:
        return stub_forecast(series, horizon, quantiles, covariate_future)

    def ready(self) -> bool:
        return True
