"""Detection sensitivity sweeps (doc 07 M6 / doc 11 M4).

Doc 07 §3.6: how strict a match must be is tunable, never hard-coded, and the
DEFAULTS come from harness calibration against the labeled replay corpus. This
module is that machinery: it re-evaluates a recorded bundle under a grid of
sensitivity settings (the Go replay binary's EVALUATION mode — explicitly never
a verification) and scores each point against ground-truth labels with
event-level precision/recall.

Definitions (event-level, documented because they ARE the metric):

  - A label is one ground-truth positive: (phenomenon, entity substring,
    [from, to]). A label is HIT (TP) if at least one finding of that phenomenon
    on a matching entity falls inside the window; otherwise it is MISSED (FN).
  - A finding of an IN-SCOPE phenomenon is CORRECT if some positive label of
    its phenomenon covers it (entity + window); otherwise it is a FALSE
    POSITIVE. Phenomena outside the label file's scope are NOT scored — the
    calibration claims nothing about them (stated in the report).
  - recall    = labels hit / labels total
  - precision = correct findings / total in-scope findings (1.0 when no
    in-scope findings exist — no claims were made, none were wrong)
  - Expected cascades score the same way on (trigger, downstream, window).

The label file is JSON next to the bundle (corpus/labels/<name>.labels.json):

  {
    "bundle": "bundle-v1",
    "phenomena_in_scope": ["PHEN_MEMORY_LEAK", ...],
    "positives": [{"phenomenon": "...", "entity_contains": "...",
                   "from": "RFC3339", "to": "RFC3339"}, ...],
    "cascades":  [{"trigger": "...", "downstream": "...",
                   "from": "RFC3339", "to": "RFC3339"}, ...]
  }
"""

from __future__ import annotations

import json
import subprocess
import tempfile
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path


def _ts(s: str) -> datetime:
    return datetime.fromisoformat(s.replace("Z", "+00:00"))


@dataclass(frozen=True)
class GridPoint:
    """One sensitivity setting. None = keep the bundle's pinned value."""

    band: float | None = None
    min_completeness: float | None = None
    well_above: float | None = None
    cascade_window: str | None = None  # Go duration string, e.g. "10m"

    def is_pinned(self) -> bool:
        return all(
            v is None
            for v in (self.band, self.min_completeness, self.well_above, self.cascade_window)
        )

    def label(self) -> str:
        if self.is_pinned():
            return "pinned"
        parts = []
        if self.band is not None:
            parts.append(f"band={self.band}")
        if self.min_completeness is not None:
            parts.append(f"min_completeness={self.min_completeness}")
        if self.well_above is not None:
            parts.append(f"well_above={self.well_above}")
        if self.cascade_window is not None:
            parts.append(f"cascade_window={self.cascade_window}")
        return " ".join(parts)


@dataclass
class PhenomenonScore:
    tp: int = 0
    fn: int = 0
    correct_findings: int = 0
    total_findings: int = 0

    @property
    def recall(self) -> float:
        total = self.tp + self.fn
        return self.tp / total if total else 1.0

    @property
    def precision(self) -> float:
        return self.correct_findings / self.total_findings if self.total_findings else 1.0


@dataclass
class SweepResult:
    point: GridPoint
    per_phenomenon: dict[str, PhenomenonScore] = field(default_factory=dict)
    cascade_hit: int = 0
    cascade_total: int = 0
    cascade_false: int = 0

    @property
    def recall(self) -> float:
        tp = sum(s.tp for s in self.per_phenomenon.values())
        fn = sum(s.fn for s in self.per_phenomenon.values())
        return tp / (tp + fn) if (tp + fn) else 1.0

    @property
    def precision(self) -> float:
        correct = sum(s.correct_findings for s in self.per_phenomenon.values())
        total = sum(s.total_findings for s in self.per_phenomenon.values())
        return correct / total if total else 1.0


def _replay_args(point: GridPoint) -> list[str]:
    if point.is_pinned():
        return []  # verification mode: the pinned regime, digests verified too
    args = ["-eval"]
    if point.band is not None:
        args += ["-eval-band", str(point.band)]
    if point.min_completeness is not None:
        args += ["-eval-min-completeness", str(point.min_completeness)]
    if point.well_above is not None:
        args += ["-eval-well-above", str(point.well_above)]
    if point.cascade_window is not None:
        args += ["-eval-cascade-window", point.cascade_window]
    return args


def evaluate(
    replay_bin: Path, bundle: Path, labels: dict, point: GridPoint, cwd: Path | None = None
) -> SweepResult:
    """Run one grid point and score it against the labels.

    The pinned point runs in VERIFICATION mode (a corpus bundle that fails
    byte-identity is corrupt and raises); every override runs in EVALUATION
    mode, which verifies nothing by design. cwd must be the repo root (the
    replay binary resolves its default ontology paths relative to it); it
    defaults to the bundle's repository root guess via the binary's parent.
    """
    in_scope = set(labels.get("phenomena_in_scope", []))
    result = SweepResult(point=point)
    for phen in sorted(in_scope):
        result.per_phenomenon[phen] = PhenomenonScore()

    if cwd is None:
        cwd = replay_bin.resolve().parent.parent  # bin/replay -> repo root

    with tempfile.TemporaryDirectory() as out:
        proc = subprocess.run(
            [str(replay_bin), "-bundle", str(bundle), "-q", "-out", out, *_replay_args(point)],
            capture_output=True,
            text=True,
            cwd=cwd,
        )
        if proc.returncode != 0:
            raise RuntimeError(
                f"replay failed at {point.label()}:\n{proc.stdout}\n{proc.stderr}"
            )

        findings: list[dict] = []
        cascades: list[dict] = []
        for tick_path in sorted(Path(out).glob("tick-*.json")):
            tick = json.loads(tick_path.read_text())
            findings.extend(tick.get("findings") or [])
            cascades.extend(tick.get("cascades") or [])

    positives = labels.get("positives", [])
    for f in findings:
        phen = f["Phenomenon"]
        if phen not in in_scope:
            continue
        score = result.per_phenomenon[phen]
        score.total_findings += 1
        at = _ts(f["EvaluatedAt"])
        entity = f.get("EntityCEI", "") + "|" + f.get("Name", "")
        for lab in positives:
            if (
                lab["phenomenon"] == phen
                and lab["entity_contains"] in entity
                and _ts(lab["from"]) <= at <= _ts(lab["to"])
            ):
                score.correct_findings += 1
                break

    for lab in positives:
        score = result.per_phenomenon.setdefault(lab["phenomenon"], PhenomenonScore())
        hit = any(
            f["Phenomenon"] == lab["phenomenon"]
            and lab["entity_contains"] in (f.get("EntityCEI", "") + "|" + f.get("Name", ""))
            and _ts(lab["from"]) <= _ts(f["EvaluatedAt"]) <= _ts(lab["to"])
            for f in findings
        )
        if hit:
            score.tp += 1
        else:
            score.fn += 1

    expected_cascades = labels.get("cascades", [])
    result.cascade_total = len(expected_cascades)
    for lab in expected_cascades:
        if any(
            c["Trigger"]["Phenomenon"] == lab["trigger"]
            and c["Downstream"]["Phenomenon"] == lab["downstream"]
            and _ts(lab["from"]) <= _ts(c["Downstream"]["EvaluatedAt"]) <= _ts(lab["to"])
            for c in cascades
        ):
            result.cascade_hit += 1
    for c in cascades:
        covered = any(
            c["Trigger"]["Phenomenon"] == lab["trigger"]
            and c["Downstream"]["Phenomenon"] == lab["downstream"]
            and _ts(lab["from"]) <= _ts(c["Downstream"]["EvaluatedAt"]) <= _ts(lab["to"])
            for lab in expected_cascades
        )
        if not covered and (
            c["Trigger"]["Phenomenon"] in in_scope or c["Downstream"]["Phenomenon"] in in_scope
        ):
            result.cascade_false += 1

    return result


def sweep(
    replay_bin: Path, bundle: Path, labels: dict, grid: list[GridPoint], cwd: Path | None = None
) -> list[SweepResult]:
    return [evaluate(replay_bin, bundle, labels, p, cwd=cwd) for p in grid]


def render_report(bundle_name: str, results: list[SweepResult]) -> str:
    """Markdown report — the M6 evidence artifact behind the shipped defaults."""
    lines = [
        f"## Sensitivity sweep — `{bundle_name}`",
        "",
        "Event-level precision/recall per grid point (definitions in",
        "`harness/src/harness/sensitivity.py`; phenomena outside the label",
        "file's scope are not scored — the calibration claims nothing about them).",
        "",
        "| setting | precision | recall | cascades hit | cascade FPs |",
        "|---|---|---|---|---|",
    ]
    for r in results:
        lines.append(
            f"| {r.point.label()} | {r.precision:.3f} | {r.recall:.3f} "
            f"| {r.cascade_hit}/{r.cascade_total} | {r.cascade_false} |"
        )
    lines.append("")
    lines.append("Per-phenomenon detail (pinned point):")
    pinned = next((r for r in results if r.point.is_pinned()), results[0])
    lines.append("")
    lines.append("| phenomenon | precision | recall | findings |")
    lines.append("|---|---|---|---|")
    for phen in sorted(pinned.per_phenomenon):
        s = pinned.per_phenomenon[phen]
        lines.append(f"| {phen} | {s.precision:.3f} | {s.recall:.3f} | {s.total_findings} |")
    lines.append("")
    return "\n".join(lines)
