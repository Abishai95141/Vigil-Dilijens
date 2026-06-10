"""clockd — Vigil's forecasting clock service (doc 09).

The clock answers exactly one question: when does this number cross that number.
It sees ONLY bare float series (scale-normalized internally); never labels, names,
units, entities, graph structure, or reasons (the charter, doc 01 §4). Everything
that makes a forecast MEAN something is authored graph knowledge, joined at the
surfacing layer, never here.

This package ships a pure-stdlib StubClock that is contract-conformant: the swap
test (doc 09 M1) requires a stub to pass the same conformance suite as the real
model. TimesFM 2.5 lands behind the `model` extra in Phase 2.
"""

from clockd.forecast import ForecastResult, StubClock, stub_forecast

__all__ = ["ForecastResult", "StubClock", "stub_forecast"]
