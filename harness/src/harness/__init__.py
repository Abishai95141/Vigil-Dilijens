"""harness — Vigil's offline validation & calibration machinery (doc 11).

The graph makes empirical claims (equivalences, temporal orders, spans); the
forecaster makes statistical claims (bands, crossings). This package tests both
BEFORE operators are exposed to them. It is offline tooling, not product — nothing
here ships into a customer cluster.

This module currently exposes the forecast-backtest primitives (doc 11 §3.5). The
replay substrate, binding-QA, and falsification suites land across Phases 0b–2.
"""

from harness.backtest import band_coverage, time_to_cross_error

__all__ = ["band_coverage", "time_to_cross_error"]
