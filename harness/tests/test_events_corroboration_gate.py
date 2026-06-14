"""Regression + adversarial tests for the events-corroboration gate (v3 T-C)."""

from __future__ import annotations

from pathlib import Path

from harness.events_corroboration_gate import gate, load_label, load_rows, score

REPO = Path(__file__).resolve().parents[2]

CUR_INST = "i|c|ns|Pod|currency|u1"
CUR_ROLE = "r|c|ns|Deployment|Deployment/currency"
CART_INST = "i|c|ns|Pod|cart|u2"
CART_ROLE = "r|c|ns|Deployment|Deployment/cart"
NODE = "i|c||Node|n1|un"


def ev_row(reason, ent, role, unresolved, corrob, why=""):
    kind = "Node" if "|Node|" in ent else "Pod"
    return {
        "event": {
            "reason": reason,
            "entityCei": ent,
            "roleCei": role,
            "roleUnresolved": unresolved,
            "kind": kind,
            "count": 1,
        },
        "corroborates": "PHEN_OOM_KILL_CGROUP" if reason == "OOMKilled" else "",
        "gaugeRoleMatch": corrob,
        "why": why,
    }


def ora(ent, role, resolved, should):
    return {
        "entityCei": ent,
        "oracleRole": role,
        "oracleResolved": resolved,
        "shouldCorroborate": should,
    }


def lbl(bundle, scenario, oracles, corr, stand, unres, digest_ok=True):
    return {
        "bundle": bundle,
        "scenario": scenario,
        "events": oracles,
        "digestBefore": "abc",
        "digestAfter": "abc" if digest_ok else "xyz",
        "expectCorroborated": corr,
        "expectStandalone": stand,
        "expectUnresolved": unres,
    }


def good_bundles() -> dict:
    return {
        "corroborated-oom": (
            [
                ev_row(
                    "OOMKilled",
                    CUR_INST,
                    CUR_ROLE,
                    False,
                    True,
                    why="co-occurs with the cgroup-OOM phenomenon",
                )
            ],
            lbl(
                "events-corroborated-oom",
                "corroborated-oom",
                [ora(CUR_INST, CUR_ROLE, True, True)],
                1,
                0,
                0,
            ),
        ),
        "multi-role": (
            [
                ev_row("OOMKilled", CUR_INST, CUR_ROLE, False, True, "co-occurs"),
                ev_row("OOMKilled", CART_INST, CART_ROLE, False, True, "co-occurs"),
            ],
            lbl(
                "events-multi-role",
                "multi-role",
                [ora(CUR_INST, CUR_ROLE, True, True), ora(CART_INST, CART_ROLE, True, True)],
                2,
                0,
                0,
            ),
        ),
        "identity-mismatch": (
            [ev_row("OOMKilled", CUR_INST, CUR_ROLE, False, False)],
            lbl(
                "events-identity-mismatch",
                "identity-mismatch",
                [ora(CUR_INST, CUR_ROLE, True, False)],
                0,
                1,
                0,
            ),
        ),
        "standalone-crashloop": (
            [
                ev_row("CrashLoopBackOff", CART_INST, CART_ROLE, False, False),
                ev_row("OOMKilled", CUR_INST, CUR_ROLE, False, False),
                ev_row("OOMKilling", NODE, NODE, True, False),
            ],
            lbl(
                "events-standalone-crashloop",
                "standalone-crashloop",
                [
                    ora(CART_INST, CART_ROLE, True, False),
                    ora(CUR_INST, CUR_ROLE, True, False),
                    ora(NODE, NODE, False, False),
                ],
                0,
                2,
                1,
            ),
        ),
        "healthy-negative": (
            [],
            lbl("events-healthy-negative", "healthy-negative", [], 0, 0, 0),
        ),
    }


def test_good_corpus_passes():
    v = gate(score(good_bundles()))
    assert v.passed and not v.insufficient, v.reasons


def test_identity_mismatch_phantom_fails():
    # Make the identity-mismatch event corroborate (a role the oracle forbids).
    b = good_bundles()
    b["identity-mismatch"][0][0]["gaugeRoleMatch"] = True
    v = gate(score(b))
    assert not v.passed and not v.insufficient
    assert any("JOIN-FIDELITY" in r or "phantom" in r for r in v.reasons)


def test_resolution_mismatch_fails():
    # The OOMKilled event resolves to the WRONG role (not the oracle's).
    b = good_bundles()
    b["corroborated-oom"][0][0]["event"]["roleCei"] = "r|c|ns|Deployment|Deployment/wrong"
    v = gate(score(b))
    assert not v.passed and not v.insufficient
    assert any("JOIN-FIDELITY" in r for r in v.reasons)


def test_event_dropped_breaks_visibility():
    # Drop a standalone event from the rows: the oracle still expects it surfaced.
    b = good_bundles()
    b["standalone-crashloop"][0].pop()  # drop the node OOM row
    v = gate(score(b))
    assert not v.passed and not v.insufficient
    assert any("visibility" in r or "dropped" in r or "counts" in r for r in v.reasons)


def test_standalone_visibility_zero_fails():
    # Make EVERY event corroborated ⇒ no standalone event is ever surfaced.
    b = good_bundles()
    for rows, _ in b.values():
        for r in rows:
            r["gaugeRoleMatch"] = True
    v = gate(score(b))
    assert not v.passed and not v.insufficient
    assert any("STANDALONE" in r for r in v.reasons)


def test_digest_change_fails():
    b = good_bundles()
    rows, label = b["corroborated-oom"]
    label["digestAfter"] = "perturbed"
    b["corroborated-oom"] = (rows, label)
    v = gate(score(b))
    assert not v.passed and not v.insufficient
    assert any("digest" in r for r in v.reasons)


def test_charter_violation_fails():
    b = good_bundles()
    b["corroborated-oom"][0][0]["why"] = (
        "the OOM was caused by a memory leak"  # generated causation
    )
    v = gate(score(b))
    assert not v.passed and not v.insufficient
    assert any("charter" in r for r in v.reasons)


def test_count_mismatch_fails():
    b = good_bundles()
    b["corroborated-oom"][1]["expectCorroborated"] = 0  # label says 0 but a row corroborates
    v = gate(score(b))
    assert not v.passed and not v.insufficient
    assert any("counts" in r for r in v.reasons)


def test_missing_scenario_insufficient():
    b = good_bundles()
    del b["healthy-negative"]
    v = gate(score(b))
    assert not v.passed and v.insufficient


def test_live_frozen_corpus_passes():
    d = REPO / "corpus" / "events"
    names = [
        "corroborated-oom",
        "multi-role",
        "identity-mismatch",
        "standalone-crashloop",
        "healthy-negative",
    ]
    if not all((d / f"events-{n}.jsonl").exists() for n in names):
        import pytest

        pytest.skip("frozen events corpus not present (run REGEN_EVENTS_CORPUS=1 go test ...)")
    bundles = {}
    for n in names:
        label = load_label(d / f"label-{n}.json")
        bundles[label["bundle"]] = (load_rows(d / f"events-{n}.jsonl"), label)
    v = gate(score(bundles))
    assert v.passed and not v.insufficient, v.reasons
