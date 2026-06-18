"""Meta-gate: every corpus directory must have a DECLARED drift-guard status.

audit roadmap #4 ("no gate corpus without a live drift guard"). This test does not run
the gates; it asserts the *coverage map is complete* — so a NEW corpus directory cannot
ship without someone declaring how it is protected from silent drift. It is the registry
that the per-gate `*FrozenConsistent` Go guards plug into.

Statuses:
  guarded        an always-on Go `*FrozenConsistent` test re-runs the REAL producer and
                 byte-locks the committed corpus (a producer change that forgets to
                 regenerate fails in CI, no cluster). The strong core.
  fixture-replay real captured / curated data graded by the Python scorer; the engine is
                 covered by independent unit tests. NOT producer-locked (a curated case may
                 not be reproducible from the current synthetic inventory — e.g. the MCP
                 silence-ledger's dark-bar). Staleness risk is accepted + documented; see
                 docs/testing/gate-rigor.md.
  non-gate       not a gate corpus (oracles/evidence, replay bundles, chaos manifests,
                 feature flags).
"""

from __future__ import annotations

import importlib.util
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
CORPUS = REPO / "corpus"
TESTS = Path(__file__).resolve().parent

GUARD_STATUS = {
    # Producer-locked, always-on Go *FrozenConsistent drift guards.
    "app-slo": "guarded",
    "departure": "guarded",
    "event-detection": "guarded",
    "transitive-chain": "guarded",
    "projected-transitive": "guarded",
    "validate-claim": "guarded",
    "events": "guarded",  # added: events.TestEventsCorpusFrozenConsistent
    "incident-memory": "guarded",  # added: replay.TestIncidentCorpusFrozenConsistent
    "mcp": "guarded",  # added: mcp.TestMCPAdvisoryCorpusFrozenConsistent (advisory-drafts);
    # silence-ledger.json within corpus/mcp is a CURATED fixture (dark-bar case), not
    # producer-locked — graded by the scorer, documented in gate-rigor.md.
    # Real captured / curated data, scorer-graded; engine in unit tests (documented weaker).
    "crossservice": "fixture-replay",
    "crossservice-projected": "fixture-replay",
    # Not gate corpora.
    "labels": "non-gate",
    "bundles": "non-gate",
    "chaos": "non-gate",
    "flags": "non-gate",
}


def test_every_corpus_dir_has_a_declared_guard_status():
    dirs = sorted(p.name for p in CORPUS.iterdir() if p.is_dir())
    unregistered = [d for d in dirs if d not in GUARD_STATUS]
    assert not unregistered, (
        f"corpus dir(s) {unregistered} have no declared drift-guard status. Add an "
        f"always-on *FrozenConsistent Go guard (see events/incident/mcp for the pattern) "
        f"and register it here, or mark it 'fixture-replay'/'non-gate' with a reason. "
        f"audit roadmap #4: no gate corpus without a declared drift guard."
    )
    # And nothing registered that has since been deleted (keep the map honest).
    stale = [d for d in GUARD_STATUS if not (CORPUS / d).is_dir()]
    assert not stale, f"GUARD_STATUS lists corpus dir(s) that no longer exist: {stale}"


def test_guarded_gate_count():
    guarded = sorted(k for k, v in GUARD_STATUS.items() if v == "guarded")
    # 6 original strong gates + events/mcp/incident hardened this track = 9.
    assert len(guarded) == 9, guarded


# Each "guarded" corpus claims an always-on Go *FrozenConsistent producer guard. This map
# names the actual Go test; the test below asserts it EXISTS in the source tree — so a
# corpus cannot be marked "guarded" without the real guard backing the claim.
GUARDED_FROZEN_TEST = {
    "app-slo": "TestAppSLOCorpusFrozenConsistent",
    "departure": "TestDepartureCorpusFrozenConsistent",
    "event-detection": "TestEventDetectCorpusFrozenConsistent",
    "transitive-chain": "TestTransitiveCorpusFrozenConsistent",
    "projected-transitive": "TestProjTransCorpusFrozenConsistent",
    "validate-claim": "TestValidateClaimCorpusFrozenConsistent",
    "events": "TestEventsCorpusFrozenConsistent",
    "incident-memory": "TestIncidentCorpusFrozenConsistent",
    "mcp": "TestMCPAdvisoryCorpusFrozenConsistent",
}


def test_guarded_status_is_backed_by_a_real_frozen_guard():
    # "guarded" must not be an empty label: every guarded corpus names a Go *FrozenConsistent
    # test, and that test must actually exist in obsd/. This is the cross-language teeth that
    # keeps the meta-gate honest — marking a corpus guarded without writing the guard fails.
    guarded = sorted(k for k, v in GUARD_STATUS.items() if v == "guarded")
    assert sorted(GUARDED_FROZEN_TEST) == guarded, (
        f"GUARDED_FROZEN_TEST must name a frozen guard for EVERY guarded corpus. "
        f"guarded={guarded}, mapped={sorted(GUARDED_FROZEN_TEST)}"
    )
    go_tests = list((REPO / "obsd").rglob("*_test.go"))
    blob = "\n".join(p.read_text(encoding="utf-8") for p in go_tests)
    for corpus, fn in sorted(GUARDED_FROZEN_TEST.items()):
        assert f"func {fn}(" in blob, (
            f"corpus/{corpus} is marked 'guarded' but its frozen guard {fn} does not exist in "
            f"obsd/**/*_test.go. Either write the always-on *FrozenConsistent Go guard, or "
            f"downgrade the corpus to 'fixture-replay' with a reason."
        )


# --- Scenario-drift guard (testing-blindspot-audit #1) -----------------------
#
# Each label-oracle gate hard-codes its scenario list in THREE independent places: the
# `justfile` recipe shell loop, the pytest module's SCENARIOS constant, and the
# corpus/<gate>/label-*.json files on disk. They are in sync today, but NOTHING failed
# if they drifted — add a corpus scenario and forget to wire the runners and coverage
# silently shrinks while CI stays green. This pins the count (forcing a conscious bump)
# AND, where a module exposes SCENARIOS, proves the runner consumes EVERY on-disk oracle.

# gate corpus dir -> the number of label-*.json oracles it must carry. Bumping a corpus
# without bumping this number (or vice-versa) fails loudly, prompting the runner wiring.
EXPECTED_SCENARIOS = {
    "app-slo": 10,
    "departure": 8,
    "event-detection": 7,
    "events": 5,
    "incident-memory": 4,
    "projected-transitive": 9,
    "transitive-chain": 6,
    "crossservice": 3,
    "crossservice-projected": 3,
}

# pytest module -> the corpus dir it grades, for the modules that expose a module-level
# SCENARIOS list (the strong cross-check: SCENARIOS must equal the on-disk oracle set).
SCENARIO_MODULES = {
    "test_app_slo_gate.py": "app-slo",
    "test_departure_gate.py": "departure",
    "test_event_detection_gate.py": "event-detection",
    "test_transitive_chain_gate.py": "transitive-chain",
    "test_projected_transitive_gate.py": "projected-transitive",
}


def _corpus_scenarios(gate: str) -> set[str]:
    """The scenario suffixes present on disk: corpus/<gate>/label-<suffix>.json."""
    return {p.name[len("label-") : -len(".json")] for p in (CORPUS / gate).glob("label-*.json")}


def _load_scenarios(module_file: str) -> list[str]:
    """Import a gate test module by path and return its module-level SCENARIOS list."""
    spec = importlib.util.spec_from_file_location(module_file[:-3], TESTS / module_file)
    assert spec and spec.loader, module_file
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return list(mod.SCENARIOS)


def test_gate_corpus_scenario_counts_are_pinned():
    # Every label-oracle gate corpus carries exactly its pinned number of scenarios; a
    # gate corpus with oracles must be registered here so its count cannot silently move.
    for gate in sorted(EXPECTED_SCENARIOS):
        n = len(list((CORPUS / gate).glob("label-*.json")))
        assert n == EXPECTED_SCENARIOS[gate], (
            f"corpus/{gate} has {n} label-*.json oracles but EXPECTED_SCENARIOS pins "
            f"{EXPECTED_SCENARIOS[gate]}. If you added/removed a scenario, bump this number "
            f"AND wire it into the justfile recipe + the pytest SCENARIOS list (testing-"
            f"blindspot-audit #1: scenario-list drift)."
        )
    # No label-oracle gate corpus may exist without a pinned count.
    unpinned = sorted(
        d
        for d, status in GUARD_STATUS.items()
        if status in ("guarded", "fixture-replay")
        and list((CORPUS / d).glob("label-*.json"))
        and d not in EXPECTED_SCENARIOS
    )
    assert not unpinned, (
        f"gate corpus dir(s) {unpinned} carry label-*.json oracles but are not pinned in "
        f"EXPECTED_SCENARIOS — coverage could silently shrink. Pin their scenario count."
    )


def test_scenario_modules_consume_every_corpus_oracle():
    # The strong teeth: for every gate whose pytest exposes a SCENARIOS list, that list
    # must equal the on-disk oracle set EXACTLY — so a corpus scenario can never be added
    # without the scorer actually grading it (and a SCENARIOS entry can never dangle).
    for module_file, gate in sorted(SCENARIO_MODULES.items()):
        scenarios = set(_load_scenarios(module_file))
        on_disk = _corpus_scenarios(gate)
        missing = on_disk - scenarios
        dangling = scenarios - on_disk
        assert not missing, (
            f"{module_file} SCENARIOS does not grade corpus/{gate} oracle(s) {sorted(missing)} "
            f"— on disk but the scorer never sees them (silent coverage loss)."
        )
        assert not dangling, (
            f"{module_file} SCENARIOS lists {sorted(dangling)} with no corpus/{gate}/label-*.json "
            f"on disk — a dangling scenario the corpus no longer backs."
        )
