"""ForecastService serving tests (doc 09 M1) — the wire half of the contract.

Need the `serve` extra (grpcio + protobuf); the base environment skips them,
stated by the skip reason, so `just clockd-test` stays hermetic. The servicer
is exercised over a real in-process grpc.aio server on an ephemeral port.
"""

from __future__ import annotations

import asyncio
import json
import pathlib

import pytest

grpc = pytest.importorskip("grpc", reason="serving tests need the `serve` extra")

from clockd.forecast import StubClock  # noqa: E402
from clockd.server import STATUS_OK, serve_async  # noqa: E402
from vigil.clock.v1 import clock_pb2, clock_pb2_grpc  # noqa: E402

CASES = json.loads(
    (pathlib.Path(__file__).parent / "conformance" / "cases.json").read_text()
)


def run(coro):
    return asyncio.run(coro)


async def with_server(fn):
    server, port = await serve_async(StubClock(), port=0)
    try:
        async with grpc.aio.insecure_channel(f"127.0.0.1:{port}") as ch:
            await fn(clock_pb2_grpc.ForecastServiceStub(ch))
    finally:
        await server.stop(None)


def test_forecast_over_the_wire_conforms():
    async def scenario(svc):
        doc = CASES
        for case in doc["cases"]:
            resp = await svc.Forecast(
                clock_pb2.ForecastRequest(
                    series=case["series"], horizon=doc["horizon"], quantiles=doc["quantiles"]
                )
            )
            assert resp.horizon == doc["horizon"]
            assert resp.num_quantiles == len(doc["quantiles"])
            assert len(resp.point) == doc["horizon"]
            assert len(resp.quantile_values) == len(doc["quantiles"]) * doc["horizon"]
            # band never collapses on the wire either (q90 row minus q10 row)
            h = doc["horizon"]
            q10 = resp.quantile_values[0:h]
            q90 = resp.quantile_values[2 * h : 3 * h]
            assert all(hi - lo > 0 for lo, hi in zip(q10, q90, strict=True))

    run(with_server(scenario))


def test_empty_input_refused():
    async def scenario(svc):
        with pytest.raises(grpc.aio.AioRpcError) as err:
            await svc.Forecast(clock_pb2.ForecastRequest(series=[], horizon=8, quantiles=[0.5]))
        assert err.value.code() == grpc.StatusCode.INVALID_ARGUMENT

        with pytest.raises(grpc.aio.AioRpcError) as err:
            await svc.Forecast(
                clock_pb2.ForecastRequest(series=[1.0, 2.0], horizon=0, quantiles=[0.5])
            )
        assert err.value.code() == grpc.StatusCode.INVALID_ARGUMENT

    run(with_server(scenario))


def test_health_reports_ready():
    async def scenario(svc):
        resp = await svc.Health(clock_pb2.HealthRequest())
        assert resp.ready is True
        assert resp.status_code == STATUS_OK

    run(with_server(scenario))
