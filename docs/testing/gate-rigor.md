# Gate rigor — an honest tier list (Track 5)

> Closes audit-v3 roadmap **#4 + #5**. The prior framework presented all corpus gates as
> equal "mathematical proof." They are NOT equal. This is the honest map of how each gate
> is protected from silent drift, what was hardened, and what remains a fixture by design.

## The tiers

**Tier 1 — producer-locked (always-on Go `*FrozenConsistent` drift guard).**
An unconditional Go test re-runs the REAL producer over the same inputs and asserts the
committed corpus is **byte-identical**. A producer change that forgets to regenerate fails
in CI, no cluster, immediately. The Python scorer then grades that engine output against an
independently hand-authored oracle. This is the strong core.

| Gate | Producer | Drift guard |
|---|---|---|
| event-detection | eventdetect.Findings | `eventdetect` frozen guard |
| app-slo | binding.Compile → observe.Materialize → detect.Matcher | `detect` frozen guard |
| departure | departure.Detect | `departure` frozen guard |
| transitive-chain | flow.TransitiveChains | `flow` frozen guard |
| projected-transitive | flow.ProjectedTransitiveChains | `flow` frozen guard |
| validate-claim | api.ValidateClaim | `api` frozen guard (**skip→fail** hardened) |
| **events** | events.Corroborate | **added: `events.TestEventsCorpusFrozenConsistent`** |
| **incident-memory** | incident.Accumulator fold | **added: `replay.TestIncidentCorpusFrozenConsistent`** |
| **mcp** (advisory-drafts) | mcp.ValidateAdvisory | **added: `mcp.TestMCPAdvisoryCorpusFrozenConsistent`** |

**Tier 2 — fixture-replay (curated/captured data, scorer-graded; engine in unit tests).**
NOT producer-locked: the committed corpus is real captured data or a curated case that the
current synthetic inventory does not reproduce, so a byte-equality drift guard is the wrong
tool. The engine semantics ARE covered by independent Go unit tests — so the risk is
*staleness*, not circularity. Stated, never hidden.

| Gate / corpus | Why fixture, not producer-locked |
|---|---|
| crossservice / crossservice-projected | real live-captured cascade data; no in-test capture pipeline to re-derive it |
| mcp `silence-ledger.json` | a CURATED ledger demonstrating the dark-bar (no-stream-key) silence the current synthetic inventory doesn't auto-produce; the gate's claims (determinism/completeness/reconciliation/dark-bar) hold on the committed ledger |

## What this track hardened (and what it caught)

- **Added always-on drift guards** for the three gates the audit flagged as guardless:
  `events`, `incident-memory`, `mcp` (advisory-drafts). Each re-runs the real producer to a
  temp dir and byte-compares the committed corpus.
- **Fixed a real non-determinism** (audit finding D): the MCP advisory regen iterated a Go
  **map** → non-deterministic row order → regen produced a different file each run, so a
  drift guard would have flaked. Now the registers are sorted; the corpus was regenerated
  deterministically (row reorder only).
- **The new silence-ledger guard immediately caught real staleness** — the committed ledger
  had drifted 38 lines from the current producer (the ontology grew v0.5.0→v0.8.0, changing
  bindings, but the corpus was never regenerated; the old skip-only test never noticed). On
  inspection the ledger is a *curated* fixture (the dark-bar case), so it is recorded as
  Tier 2 rather than silently "fixed" — but the episode is exactly why drift guards matter.
- **skip→fail on a missing golden** (audit #5): the Go validate guard and the four Python
  frozen-corpus tests (validate / events / incident / mcp) now **fail** (never skip) when
  the committed corpus is absent — a deleted golden turns CI red, not green.
- **A meta-gate** (`harness/tests/test_gate_coverage.py`) asserts every `corpus/` directory
  has a *declared* drift-guard status, so a new gate corpus cannot ship without someone
  saying how it's protected.

## Honest bottom line

Of the 11 corpus gates: **9 are now producer-locked** (6 original + events/incident/mcp);
**2 (crossservice, crossservice-projected) are fixture-replay** with the engine covered by
unit tests; the mcp silence-ledger is a curated fixture inside an otherwise-guarded gate.
That is the accurate claim — not "11 equal proofs."
