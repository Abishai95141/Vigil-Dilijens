"""Forecast backtest primitives (doc 11 §3.5) — the gate between the clock and the
operator (doc 09 M3).

These are the measurements the calibration gate is built on:
  - band_coverage: over held-out history, do the X% quantile bands contain the
    realized values X% of the time? Mis-calibration in EITHER direction fails a class.
  - time_to_cross_error: for realized crossings, the (projected − actual) error.

Pure numpy; no plotting/IO here so the core math is trivially testable.
"""

from __future__ import annotations

import numpy as np
from numpy.typing import ArrayLike


def band_coverage(realized: ArrayLike, lower: ArrayLike, upper: ArrayLike) -> float:
    """Fraction of realized values that fall within [lower, upper], inclusive.

    A well-calibrated q-width band (e.g. the 10–90% band, nominal width 0.8) should
    return a coverage close to its nominal level over enough samples.
    """
    r = np.asarray(realized, dtype=float)
    lo = np.asarray(lower, dtype=float)
    hi = np.asarray(upper, dtype=float)
    if not (r.shape == lo.shape == hi.shape):
        raise ValueError(f"shape mismatch: realized {r.shape}, lower {lo.shape}, upper {hi.shape}")
    if r.size == 0:
        raise ValueError("cannot compute coverage over an empty sample")
    if np.any(hi < lo):
        raise ValueError("upper bound is below lower bound for at least one sample")
    inside = (r >= lo) & (r <= hi)
    return float(np.count_nonzero(inside) / r.size)


def time_to_cross_error(projected_cross: float, actual_cross: float) -> float:
    """Signed projection error for a realized crossing, in the same time units as
    the inputs. Positive => the forecast was LATE (projected after actual); negative
    => EARLY. The backtest aggregates the distribution of this over a class.
    """
    return float(projected_cross - actual_cross)
