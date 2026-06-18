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

from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
CORPUS = REPO / "corpus"

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
