"""Forecast backtest suite + calibration gate (doc 11 §3.5 M5 / doc 09 M3).

Builds on the pure primitives in backtest.py (band_coverage,
time_to_cross_error). The gate between the clock and the operator: per TARGET
CLASS, over recorded bundles, score the REAL forecast pipeline's output against
the realized future from the same capture. NO warning class becomes
operator-visible until its gate passes; a clock swap/upgrade re-runs every
shipped class's gate.

Inputs (both produced from ONE bundle):
  - events JSONL from `replay -forecast`: per-tick TickForecast records —
    every invocation's raw trajectories (point + quantile bands) and verdict,
    plus the guard silences. The replay engine ran the REAL funnel, budget,
    and runner "as of" each tick (doc 11 §3.1 time-shift, made executable).
  - readings Parquet from `replay -export-parquet`: the realized series.

Scores (doc 11 §3.5): band coverage (mis-calibration in EITHER direction
fails); time-to-cross error point + band membership; silence correctness;
sample-size honesty (a thin corpus yields INSUFFICIENT, never a pass).

v1 gate criteria are initial values with rationale (constants below); they
harden as the corpus grows (doc 11 §6 held-out splits).
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path

import numpy as np
import pyarrow.parquet as pq

from harness.backtest import band_coverage, time_to_cross_error

# --- v1 gate criteria (container working-set → OOM class) -------------------
# Nominal band mass for [q10, q90] is 0.8. The acceptance window is wider on
# the high side (over-coverage wastes sharpness but does not lie) and tighter
# on the low side (under-coverage = overconfident bands — the trust-killing
# failure, doc 00 §8). Recall / false-warning floors are modest for the v1
# corpus; the gate exists to BLOCK shipping when the clock is wrong, not to
# flatter it.
BAND_COVERAGE_MIN = 0.65
BAND_COVERAGE_MAX = 0.98
RECALL_MIN = 0.70
FALSE_WARNING_MAX = 0.30
TTC_MEDIAN_FRAC_MAX = 0.35  # median |ttc error| ≤ 35% of actual time-to-cross
MIN_SCORED_FORECASTS = 5


@dataclass
class ClassReport:
    """Backtest scores for one target class (one canonical metric)."""

    metric: str
    forecasts_scored: int = 0
    forecasts_skipped_short_future: int = 0

    realized: list = field(default_factory=list)  # accumulated (value, lo, hi)
    lo: list = field(default_factory=list)
    hi: list = field(default_factory=list)

    actual_crossings: int = 0
    warned_crossings: int = 0
    missed_crossings: int = 0
    misses_by_reason: dict = field(default_factory=dict)
    false_warnings: int = 0
    candidates_full_obs: int = 0
    crossings_in_band: int = 0

    ttc_errors_steps: list = field(default_factory=list)
    ttc_frac_errors: list = field(default_factory=list)

    silences_correct: int = 0

    @property
    def band_points(self) -> int:
        return len(self.realized)

    @property
    def coverage(self) -> float:
        if not self.realized:
            return float("nan")
        return band_coverage(self.realized, self.lo, self.hi)

    @property
    def recall(self) -> float:
        return (
            self.warned_crossings / self.actual_crossings
            if self.actual_crossings
            else float("nan")
        )

    @property
    def false_rate(self) -> float:
        return (
            self.false_warnings / self.candidates_full_obs
            if self.candidates_full_obs
            else float("nan")
        )

    @property
    def ttc_median_frac(self) -> float:
        if not self.ttc_frac_errors:
            return float("nan")
        return float(np.median(self.ttc_frac_errors))


def load_events(path: str | Path) -> list[dict]:
    return [json.loads(line) for line in Path(path).read_text().splitlines() if line.strip()]


def load_readings(path: str | Path) -> dict[tuple[str, str], list[tuple[int, float]]]:
    """(uid, metric) -> [(at_unix_nano, value)] sorted by time."""
    table = pq.read_table(path, columns=["uid", "metric", "at_unix_nano", "value"])
    out: dict[tuple[str, str], list[tuple[int, float]]] = {}
    for uid, metric, at, val in zip(
        table.column("uid").to_pylist(),
        table.column("metric").to_pylist(),
        table.column("at_unix_nano").to_pylist(),
        table.column("value").to_pylist(),
        strict=True,
    ):
        out.setdefault((uid, metric), []).append((at, val))
    for series in out.values():
        series.sort()
    return out


def _crossed(value: float, bar: float, direction: str) -> bool:
    return value <= bar if direction == "below" else value >= bar


def _iso_to_ns(ts: str) -> int:
    return int(datetime.fromisoformat(ts.replace("Z", "+00:00")).timestamp() * 1e9)


def score_class(
    events: list[dict],
    readings: dict[tuple[str, str], list[tuple[int, float]]],
    metric: str,
    min_future: int = 8,
) -> ClassReport:
    """Score every invocation trace of `metric` against the realized future.

    Realized step h is aligned to the h-th recorded sample after the basis
    stamp (the forecast cadence IS the scrape cadence in the bundle)."""
    rep = ClassReport(metric=metric)
    for tick in events:
        for tr in tick.get("traces") or []:
            if tr["metric"] != metric:
                continue
            series = readings.get((tr["streamUid"], metric))
            if not series:
                continue
            basis_ns = _iso_to_ns(tr["basisAt"])
            future = [(at, v) for at, v in series if at > basis_ns]
            point = tr["point"]
            horizon = len(point)
            steps = min(horizon, len(future))
            if steps < min_future:
                rep.forecasts_skipped_short_future += 1
                continue
            rep.forecasts_scored += 1

            lo_band, hi_band = tr["bands"][0], tr["bands"][-1]
            for h in range(steps):
                rep.realized.append(future[h][1])
                rep.lo.append(lo_band[h])
                rep.hi.append(hi_band[h])

            bar, direction = tr["barValue"], tr["direction"]
            actual_idx = next(
                (h for h in range(steps) if _crossed(future[h][1], bar, direction)), None
            )
            cand = tr.get("candidate")
            full_obs = len(future) >= horizon

            if actual_idx is not None:
                rep.actual_crossings += 1
                if cand:
                    rep.warned_crossings += 1
                    rep.candidates_full_obs += 1
                    cand_idx = round(cand["timeToCross"] / tick["cadence"]) - 1
                    err = time_to_cross_error(cand_idx, actual_idx)
                    rep.ttc_errors_steps.append(err)
                    rep.ttc_frac_errors.append(abs(err) / max(actual_idx + 1, 1))
                    actual_ns = future[actual_idx][0]
                    in_band = actual_ns >= _iso_to_ns(cand["earliestAt"]) and (
                        cand["latestBeyondHorizon"]
                        or actual_ns <= _iso_to_ns(cand["latestAt"])
                    )
                    if in_band:
                        rep.crossings_in_band += 1
                else:
                    rep.missed_crossings += 1
                    reason = tr.get("silence") or "unknown"
                    rep.misses_by_reason[reason] = rep.misses_by_reason.get(reason, 0) + 1
            else:
                if cand and full_obs:
                    rep.candidates_full_obs += 1
                    rep.false_warnings += 1
                elif not cand:
                    rep.silences_correct += 1
    return rep


@dataclass
class GateVerdict:
    passed: bool
    insufficient: bool
    reasons: list


def gate(rep: ClassReport) -> GateVerdict:
    """The doc 11 §3.5 gate rule for one class. INSUFFICIENT (thin corpus) is
    never a pass — a class ships on evidence, not on absence of evidence."""
    if rep.forecasts_scored < MIN_SCORED_FORECASTS:
        return GateVerdict(
            False,
            True,
            [f"insufficient corpus: {rep.forecasts_scored} scored < {MIN_SCORED_FORECASTS}"],
        )
    reasons: list[str] = []
    cov = rep.coverage
    if not (BAND_COVERAGE_MIN <= cov <= BAND_COVERAGE_MAX):
        reasons.append(
            f"band coverage {cov:.3f} outside [{BAND_COVERAGE_MIN}, {BAND_COVERAGE_MAX}]"
        )
    if rep.actual_crossings > 0 and rep.recall < RECALL_MIN:
        reasons.append(f"crossing recall {rep.recall:.3f} < {RECALL_MIN}")
    if rep.candidates_full_obs > 0 and rep.false_rate > FALSE_WARNING_MAX:
        reasons.append(f"false-warning rate {rep.false_rate:.3f} > {FALSE_WARNING_MAX}")
    if rep.ttc_frac_errors and rep.ttc_median_frac > TTC_MEDIAN_FRAC_MAX:
        reasons.append(
            f"median |ttc error| fraction {rep.ttc_median_frac:.3f} > {TTC_MEDIAN_FRAC_MAX}"
        )
    return GateVerdict(passed=not reasons, insufficient=False, reasons=reasons)


def render(rep: ClassReport, verdict: GateVerdict) -> str:
    lines = [
        f"forecast backtest — class: {rep.metric}",
        f"  scored forecasts: {rep.forecasts_scored}"
        f" (skipped short-future: {rep.forecasts_skipped_short_future})",
        f"  band coverage:    {rep.coverage:.3f} over {rep.band_points}"
        " realized points (nominal 0.8)",
        f"  crossings:        {rep.actual_crossings} actual · {rep.warned_crossings} warned"
        f" · {rep.missed_crossings} missed {rep.misses_by_reason or ''}",
        f"  in-band:          {rep.crossings_in_band}/{rep.warned_crossings}"
        " actual crossings inside [earliest, latest]",
        f"  false warnings:   {rep.false_warnings}/{rep.candidates_full_obs}"
        " (full-horizon observations)",
    ]
    if rep.ttc_errors_steps:
        lines.append(
            f"  ttc error:        median {np.median(rep.ttc_errors_steps):+.0f} steps"
            f" · median |frac| {rep.ttc_median_frac:.3f}"
        )
    lines.append(
        f"  silences:         {rep.silences_correct} correct"
        f" · {rep.missed_crossings} hid a real crossing"
    )
    if verdict.insufficient:
        lines.append(f"  GATE: INSUFFICIENT — {'; '.join(verdict.reasons)} (never a pass)")
    elif verdict.passed:
        lines.append("  GATE: PASSED — class may become operator-visible (doc 11 §3.5)")
    else:
        lines.append(f"  GATE: FAILED — {'; '.join(verdict.reasons)}")
    return "\n".join(lines)


def main() -> int:
    ap = argparse.ArgumentParser(description="forecast backtest gate (doc 11 M5 / 09 M3)")
    ap.add_argument("--events", required=True, help="JSONL from `replay -forecast`")
    ap.add_argument("--readings", required=True, help="Parquet from `replay -export-parquet`")
    ap.add_argument("--metric", default="container_memory_working_set_bytes")
    args = ap.parse_args()

    rep = score_class(load_events(args.events), load_readings(args.readings), args.metric)
    verdict = gate(rep)
    print(render(rep, verdict))
    return 0 if verdict.passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
