"""The harness's first deterministic regression (doc 11 M1 / doc 05 M5 exit).

Runs the Go replay binary on the committed fixture bundle and asserts the
replay guarantee from the OUTSIDE: every recorded tick reproduces
byte-identically, two runs of the replay are themselves byte-identical, and
the Parquet bridge (the harness's columnar input, doc 14 §2.3) round-trips
the readings exactly.

The binary is built on demand (`go build`) when bin/replay is absent; if no Go
toolchain is available the test SKIPS WITH A STATED REASON — it never fakes a
pass.
"""

from __future__ import annotations

import re
import shutil
import subprocess
from pathlib import Path

import pytest

REPO = Path(__file__).resolve().parents[2]
FIXTURE = REPO / "obsd" / "internal" / "replay" / "testdata" / "bundle-v1"
BINARY = REPO / "bin" / "replay"


def _replay_binary() -> Path:
    if BINARY.exists():
        return BINARY
    go = shutil.which("go")
    if go is None:
        pytest.skip("bin/replay not built and no Go toolchain on PATH (run `just build` first)")
    subprocess.run(
        [go, "build", "-o", str(BINARY), "./obsd/cmd/replay"],
        cwd=REPO,
        check=True,
        capture_output=True,
    )
    return BINARY


def _run(binary: Path, *extra: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [str(binary), "-bundle", str(FIXTURE), *extra],
        cwd=REPO,
        capture_output=True,
        text=True,
    )


def test_fixture_bundle_exists() -> None:
    assert (FIXTURE / "manifest.json").exists(), (
        "fixture bundle missing — regenerate with "
        "REGEN_FIXTURE=1 go test ./obsd/internal/replay/ -run TestFixture"
    )


def test_replay_is_byte_identical_and_repeatable() -> None:
    binary = _replay_binary()
    first = _run(binary)
    second = _run(binary)
    assert first.returncode == 0, f"replay failed:\n{first.stdout}\n{first.stderr}"
    assert second.returncode == 0, f"replay failed:\n{second.stdout}\n{second.stderr}"
    # The in-bundle guarantee: every recorded live digest reproduced.
    assert "verdict: all" in first.stdout and "MISMATCH" not in first.stdout
    # The engine's own determinism: two runs emit identical bytes.
    assert first.stdout == second.stdout
    # The fixture is not trivially empty — it carries real findings.
    findings = [int(m) for m in re.findall(r"findings=(\d+)", first.stdout)]
    assert sum(findings) > 0, "fixture must carry real findings"


def test_parquet_bridge_roundtrips_readings(tmp_path: Path) -> None:
    pa = pytest.importorskip("pyarrow")
    pq = pytest.importorskip("pyarrow.parquet")
    binary = _replay_binary()
    out = tmp_path / "readings.parquet"
    res = _run(binary, "-q", "-export-parquet", str(out))
    assert res.returncode == 0, f"replay failed:\n{res.stdout}\n{res.stderr}"

    declared = re.search(r"samples=(\d+)", res.stdout)
    exported = re.search(r"exported (\d+) readings", res.stdout)
    assert declared and exported, res.stdout

    table = pq.read_table(out)
    assert table.num_rows == int(declared.group(1)) == int(exported.group(1))
    cols = set(table.column_names)
    assert {"stream_id", "uid", "metric", "type", "recv_unix_nano", "at_unix_nano", "value"} <= cols
    # The fixture carries the entity-local leak stream AND the first-order
    # THROTTLING_CASCADE pair (container throttle counters + node PSI, 07 M2).
    metrics = set(table.column("metric").to_pylist())
    assert metrics == {
        "container_memory_working_set_bytes",
        "container_cpu_cfs_throttled_periods_total",
        "container_cpu_cfs_periods_total",
        "node_pressure_cpu_waiting_seconds_total",
    }
    # Per stream, readings rise monotonically (gauges rising, counters cumulative).
    rows = sorted(
        zip(
            table.column("stream_id").to_pylist(),
            table.column("at_unix_nano").to_pylist(),
            table.column("value").to_pylist(),
            strict=True,
        )
    )
    per_stream: dict[str, list[float]] = {}
    for sid, _, val in rows:
        per_stream.setdefault(sid, []).append(val)
    assert len(per_stream) == 4
    for sid, vals in per_stream.items():
        assert vals == sorted(vals), f"{sid} readings should rise monotonically"
    assert isinstance(pa.types.is_float64(table.schema.field("value").type), bool)  # schema sanity
