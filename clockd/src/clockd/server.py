"""gRPC serving for ForecastService (doc 09 §3.1, techstack §5).

The service is a stateless function over float arrays: it knows nothing about
entities, bars, phenomena, or reasons — those live in obsd and join only at the
surfacing layer. The proto carries NO string fields (charter, conformance-tested
on the Go side); this module keeps gRPC error details terse and semantics-free.

The clock behind the servicer is swappable (doc 09 §3.1 — the contract is the
architecture, the model is a part): `--clock stub` serves the honest pure-stdlib
StubClock; `--clock timesfm` serves the pinned TimesFM 2.5 reference adapter
(doc 14 A15). The swap test runs the SAME conformance fixtures against both.

Serving deps (grpcio + protobuf runtime) live behind the `serve` extra so the
base install — and `just clockd-test` — stay hermetic; imports are lazy.
"""

from __future__ import annotations

import argparse
import asyncio
from typing import Protocol

from clockd.forecast import ForecastResult, StubClock

# Health status codes (numeric on the wire — never a string, doc 14 A13).
STATUS_OK = 0
STATUS_LOADING = 1
STATUS_FAILED = 2


class Clock(Protocol):
    """The swap contract every clock implementation satisfies (doc 09 §3.1)."""

    def forecast(
        self,
        series: list[float],
        horizon: int,
        quantiles: list[float],
        covariate_future: list[float] | None = None,
    ) -> ForecastResult: ...

    def ready(self) -> bool: ...


def build_clock(kind: str) -> Clock:
    """Construct the named clock. "stub" is always available; "timesfm" needs
    the `model` extra (pins per doc 14 A15 — package 2.0.x, model 2.5) and
    loads the checkpoint eagerly so Health reflects real readiness."""
    if kind == "stub":
        return StubClock()
    if kind == "timesfm":
        from clockd.timesfm_clock import TimesFMClock

        return TimesFMClock()
    raise ValueError(f"unknown clock kind: {kind!r} (stub|timesfm)")


def _grpc_modules():
    """Lazy import of grpc + generated stubs (behind the `serve` extra)."""
    import grpc

    from vigil.clock.v1 import clock_pb2, clock_pb2_grpc

    return grpc, clock_pb2, clock_pb2_grpc


def make_servicer(clock: Clock):
    """Build the ForecastService servicer instance around `clock`.

    Defined inside a factory (not at module top level) so importing clockd.server
    never requires grpc — only serving does.
    """
    grpc, clock_pb2, clock_pb2_grpc = _grpc_modules()

    class ForecastServicer(clock_pb2_grpc.ForecastServiceServicer):
        async def Forecast(self, request, context):  # noqa: N802 (gRPC method name)
            series = list(request.series)
            horizon = int(request.horizon)
            quantiles = list(request.quantiles)
            covariate = list(request.covariate_future) or None
            if not series or horizon <= 0 or not quantiles:
                await context.abort(grpc.StatusCode.INVALID_ARGUMENT, "empty input")
            try:
                result = clock.forecast(series, horizon, quantiles, covariate)
            except NotImplementedError:
                await context.abort(grpc.StatusCode.UNIMPLEMENTED, "unsupported")
            except ValueError:
                await context.abort(grpc.StatusCode.INVALID_ARGUMENT, "invalid input")
            return clock_pb2.ForecastResponse(
                point=result.point,
                quantile_values=result.quantile_values,
                num_quantiles=result.num_quantiles,
                horizon=result.horizon,
            )

        async def Health(self, request, context):  # noqa: N802
            ok = clock.ready()
            return clock_pb2.HealthResponse(
                ready=ok, status_code=STATUS_OK if ok else STATUS_LOADING
            )

    return ForecastServicer()


async def serve_async(clock: Clock, host: str = "127.0.0.1", port: int = 50051):
    """Start the grpc.aio server; returns (server, bound_port). Caller awaits
    termination. port=0 binds an ephemeral port (tests)."""
    grpc, _, clock_pb2_grpc = _grpc_modules()
    server = grpc.aio.server()
    clock_pb2_grpc.add_ForecastServiceServicer_to_server(make_servicer(clock), server)
    bound = server.add_insecure_port(f"{host}:{port}")
    await server.start()
    return server, bound


def main() -> None:
    p = argparse.ArgumentParser(description="vigil clockd — the forecasting clock service")
    p.add_argument("--host", default="127.0.0.1")
    p.add_argument("--port", type=int, default=50051)
    p.add_argument("--clock", default="stub", choices=["stub", "timesfm"])
    args = p.parse_args()

    async def run() -> None:
        clock = build_clock(args.clock)
        server, bound = await serve_async(clock, args.host, args.port)
        print(f"clockd serving clock={args.clock} on {args.host}:{bound}", flush=True)
        await server.wait_for_termination()

    asyncio.run(run())


if __name__ == "__main__":
    main()
