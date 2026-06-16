"""Regression tests for the projected-transitive gate scorer (doc 15 cap. D).

The frozen corpus must PASS; every floor must FAIL on a targeted mutation. The
BAND-MONOTONICITY absolute-zero floor (band must never narrow downstream) gets the most
attention.
"""

from __future__ import annotations

import copy
from pathlib import Path

from harness.projected_transitive_gate import gate, load_label, load_rows, score

CORPUS = Path(__file__).resolve().parents[2] / "corpus" / "projected-transitive"
SCENARIOS = [
    "linear-2hop", "fan-out", "two-roots", "open-horizon", "no-caller", "no-forecast",
    "midnight-wrap", "tight-root", "maxhops-bounded",
]


def _bundles() -> dict:
    b = {}
    for s in SCENARIOS:
        label = load_label(CORPUS / f"label-{s}.json")
        b[label["bundle"]] = (load_rows(CORPUS / f"chains-{s}.jsonl"), label)
    return b


def test_frozen_corpus_passes():
    v = gate(score(_bundles()))
    assert v.passed, v.reasons
    assert not v.insufficient


def test_band_narrowing_fails():
    # Tighten the hop-2 band (later earliest, earlier latest) so it no longer contains hop-1.
    # Edges are RFC3339; hop-1 is [..12:20:00Z, ..13:00:00Z], so [12:30,12:50] narrows inside it.
    b = _bundles()
    chains, label = b["projtrans-linear-2hop"]
    bad = copy.deepcopy(chains)
    h2 = bad[0]["path"][1]["band"]
    h2["earliest"] = "2026-06-16T12:30:00Z"  # later than hop1 earliest (narrowing the near side)
    h2["latest"] = "2026-06-16T12:50:00Z"    # earlier than hop1 latest (narrowing the far side)
    b["projtrans-linear-2hop"] = (bad, label)
    v = gate(score(b))
    assert not v.passed
    assert any("BAND-MONOTONICITY" in r for r in v.reasons)


def test_band_collapse_fails():
    # A band collapsed to a line (earliest == latest, not open) must trip the width floor —
    # a self-containing point would otherwise slip the monotonicity check (doc 01).
    b = _bundles()
    chains, label = b["projtrans-linear-2hop"]
    bad = copy.deepcopy(chains)
    h1 = bad[0]["path"][0]["band"]
    h1["latest"] = h1["earliest"]  # collapse to a single point
    h1["open"] = False
    b["projtrans-linear-2hop"] = (bad, label)
    v = gate(score(b))
    assert not v.passed
    assert any("BAND-WIDTH" in r for r in v.reasons)


def test_midnight_wrap_corpus_passes():
    # The midnight-straddling scenario must PASS — the producer + gate compare RFC3339 edges,
    # so a band widening past 00:00Z is correctly judged to widen (not narrow).
    v = gate(score(_bundles()))
    assert v.passed, v.reasons


def test_projected_class_fails():
    b = _bundles()
    chains, label = b["projtrans-linear-2hop"]
    bad = copy.deepcopy(chains)
    bad[0]["path"][0]["band"]["class"] = "MEASURED"  # class laundering
    b["projtrans-linear-2hop"] = (bad, label)
    v = gate(score(b))
    assert not v.passed
    assert any("PROJECTED-CLASS" in r for r in v.reasons)


def test_one_root_per_chain_fails():
    b = _bundles()
    chains, label = b["projtrans-linear-2hop"]
    bad = copy.deepcopy(chains)
    bad[0]["path"][0]["band"]["hops_from_root"] = 0  # a downstream claiming to be the root
    b["projtrans-linear-2hop"] = (bad, label)
    v = gate(score(b))
    assert not v.passed
    assert any("ONE-ROOT-PER-CHAIN" in r for r in v.reasons)


def test_chain_count_fails():
    b = _bundles()
    chains, label = b["projtrans-linear-2hop"]
    b["projtrans-linear-2hop"] = (chains + copy.deepcopy(chains), label)
    v = gate(score(b))
    assert not v.passed
    assert any("CHAIN-COUNT" in r for r in v.reasons)


def test_path_fidelity_fails():
    b = _bundles()
    chains, label = b["projtrans-linear-2hop"]
    bad = copy.deepcopy(chains)
    bad[0]["path"][0]["downstream"] = "traffic/somewhere-else"
    b["projtrans-linear-2hop"] = (bad, label)
    v = gate(score(b))
    assert not v.passed
    assert any("PATH-FIDELITY" in r for r in v.reasons)


def test_charter_fails_on_propagation_verb():
    # A PROJECTED cascade must never ASSERT the ripple — 'propagates' is banned in scaffolding.
    b = _bundles()
    chains, label = b["projtrans-linear-2hop"]
    bad = copy.deepcopy(chains)
    bad[0]["node_basis"] = "the fault propagates downstream"
    b["projtrans-linear-2hop"] = (bad, label)
    v = gate(score(b))
    assert not v.passed
    assert any("charter" in r.lower() for r in v.reasons)


def test_charter_ignores_authored_why():
    # The curated relation `why` legitimately says "propagates" — verbatim, excluded from the scan.
    v = gate(score(_bundles()))
    assert v.passed, v.reasons  # the frozen why already contains "propagates"; must still pass


def test_missing_scenario_is_insufficient():
    b = _bundles()
    del b["projtrans-no-forecast"]
    v = gate(score(b))
    assert not v.passed
    assert v.insufficient
