"""Regression tests for the transitive-chain gate scorer (doc 15 cap. B).

The frozen corpus must PASS; every floor must FAIL on a targeted mutation — a scorer that
cannot fail is not a gate. The CARDINAL floor (no false chain on coincident faults) gets
the most attention.
"""

from __future__ import annotations

from pathlib import Path

from harness.transitive_chain_gate import gate, load_label, load_rows, score

CORPUS = Path(__file__).resolve().parents[2] / "corpus" / "transitive-chain"
SCENARIOS = [
    "linear-chain",
    "independent-faults",
    "silent-intermediate",
    "fan-in",
    "two-disjoint-real-chains",
    "healthy-no-degradation",
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


def test_cardinal_false_chain_fails():
    # Forge a chain into the independent-faults scenario that links the two unrelated faults.
    b = _bundles()
    _, label = b["transitive-independent-faults"]
    forged = {
        "most_upstream_degraded_node": "a/lonely1",
        "path": [{"upstream": "a/lonely1", "downstream": "b/lonely2", "why_class": "AUTHORED"}],
        "symptoms": [{"workload": "a/lonely1"}, {"workload": "b/lonely2"}],
    }
    b["transitive-independent-faults"] = ([forged], label)
    v = gate(score(b))
    assert not v.passed
    assert any("NO-FALSE-CHAIN" in r for r in v.reasons)


def test_silent_intermediate_bridge_fails():
    # Bridging the silent intermediate links inference+prediction — a cardinal violation.
    b = _bundles()
    _, label = b["transitive-silent-intermediate"]
    bridged = {
        "most_upstream_degraded_node": "traffic/inference",
        "path": [{"upstream": "traffic/inference", "downstream": "traffic/prediction"}],
        "symptoms": [{"workload": "traffic/inference"}, {"workload": "traffic/prediction"}],
    }
    b["transitive-silent-intermediate"] = ([bridged], label)
    v = gate(score(b))
    assert not v.passed
    assert any("NO-FALSE-CHAIN" in r for r in v.reasons)


def test_chain_count_fails():
    # Duplicate the linear chain → 2 emitted != 1 expected (and roots/path stay consistent).
    b = _bundles()
    chains, label = b["transitive-linear-chain"]
    b["transitive-linear-chain"] = (chains + chains, label)
    v = gate(score(b))
    assert not v.passed
    assert any("CHAIN-COUNT" in r for r in v.reasons)


def test_root_fidelity_fails():
    b = _bundles()
    chains, label = b["transitive-linear-chain"]
    bad = [dict(chains[0])]
    bad[0]["most_upstream_degraded_node"] = "traffic/prediction"  # wrong root
    b["transitive-linear-chain"] = (bad, label)
    v = gate(score(b))
    assert not v.passed
    assert any("ROOT-FIDELITY" in r for r in v.reasons)


def test_path_fidelity_fails():
    b = _bundles()
    chains, label = b["transitive-linear-chain"]
    bad = [dict(chains[0])]
    bad[0] = dict(bad[0])
    bad[0]["path"] = [dict(s) for s in bad[0]["path"]]
    bad[0]["path"][0]["downstream"] = "traffic/somewhere-else"  # invented hop target
    b["transitive-linear-chain"] = (bad, label)
    v = gate(score(b))
    assert not v.passed
    assert any("PATH-FIDELITY" in r for r in v.reasons)


def test_charter_fails():
    # The forbidden token must be caught in SYSTEM-GENERATED scaffolding (not the authored
    # `why`, which is curated verbatim and excluded from the guard). Inject into node_basis.
    b = _bundles()
    chains, label = b["transitive-linear-chain"]
    bad = [dict(chains[0])]
    bad[0]["node_basis"] = "the upstream caused the downstream"
    b["transitive-linear-chain"] = (bad, label)
    v = gate(score(b))
    assert not v.passed
    assert any("charter" in r.lower() for r in v.reasons)


def test_charter_ignores_authored_why():
    # A curator's verbatim authored note containing a denylisted word must NOT trip the
    # guard — the why is governance-reviewed and surfaced verbatim (excluded from the scan).
    b = _bundles()
    chains, label = b["transitive-linear-chain"]
    ok = [dict(chains[0])]
    ok[0]["path"] = [dict(s) for s in ok[0]["path"]]
    ok[0]["path"][0]["why"] = "downstream latency is due to the upstream saturation (authored)"
    b["transitive-linear-chain"] = (ok, label)
    v = gate(score(b))
    assert v.passed, v.reasons


def test_missing_scenario_is_insufficient():
    b = _bundles()
    del b["transitive-healthy-no-degradation"]
    v = gate(score(b))
    assert not v.passed
    assert v.insufficient
