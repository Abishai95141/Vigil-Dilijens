"""gRPC server for ForecastService (doc 09 §3.1).

Phase-2 placeholder. The wire stubs are generated from proto/vigil/clock/v1/clock.proto
when the forecasting layer lands (doc 09 M1); this module will then expose StubClock
(and later the TimesFM clock) over `grpc.aio`. The serving deps live behind the
`model` extra so the base install — and the conformance tests — stay light.

Until then the testable, swappable unit is `clockd.forecast.StubClock`.
"""

from __future__ import annotations


def serve(host: str = "127.0.0.1", port: int = 50051) -> None:  # pragma: no cover
    raise NotImplementedError(
        "clockd gRPC serving lands in Phase 2 (doc 09 M1): generate stubs from "
        "proto/vigil/clock/v1/clock.proto, wire StubClock, then the TimesFM clock. "
        "The current swappable unit is clockd.forecast.StubClock."
    )
