# transitive-chain gate — evidence (doc 15 cap. B — the transitive root-cause chain)

**Verdict: PASSED** (`just transitive-chain-gate`, exit 0) — 2026-06-16.

## What it certifies

The one-hop cross-service cascade made **TRANSITIVE**: MEASURED-degraded workloads stitched
into an **ordered** root-cause chain by walking the OBSERVED-flow topology, oriented **ONLY**
by the AUTHORED relation (never by timing). `flow.TransitiveChains` is a pure function of
(flow topology, degraded set, relation, window), so the frozen corpus reproducing
byte-identically IS the off-digest determinism proof (doc 15 cap. B §3).

Each scenario folds the **REAL** `flow.TransitiveChains` path against a **LABEL ORACLE** fixed
by construction. JOIN, never FUSE: each node carries its OWN measured phenomenon, each edge is
the MEASURED observed flow, the only "why" is the verbatim AUTHORED relation note. No "X caused Y".

## Floors (all green)

| Floor | Result |
|---|---|
| **NO-FALSE-CHAIN == 0** (CARDINAL) | 0 — no single chain ever contains both members of an independent pair. The `independent-faults` and `two-disjoint-real-chains` scenarios prove coincident-but-unrelated faults NEVER merge; `silent-intermediate` proves a pair separated by an unmeasured node is never bridged (weakest-input) |
| **CHAIN-COUNT** | OK — the emitted chain count equals the oracle's `expectChains` (disconnected components stay separate) |
| **ROOT-FIDELITY** | OK — each chain's root is the structural root (deepest degraded callee) |
| **PATH-FIDELITY** | OK — the emitted ordered path edges equal the authored-reach (no missing hop, no invented hop) |
| **CHARTER == 0** | 0 — no chain restates a cause |

## Scenarios (corpus/transitive-chain/, 6 total)

- `linear-chain` — the smart-traffic chain `inference(mem) → aggregation(queue) → db(latency) →
  prediction(stale)`, all degraded + flow-connected → ONE ordered 3-hop chain (the headline).
- `independent-faults` — two coincident faults in DISJOINT flow components → **0 chains** (CARDINAL).
- `silent-intermediate` — `inference(deg) ← aggregation(SILENT) ← prediction(deg)` → **0 chains**,
  never bridged across the unmeasured node; the silence is a stated Gap.
- `fan-in` — one degraded callee impacts TWO degraded callers → 1 chain, 2 hop-1 steps.
- `two-disjoint-real-chains` — two genuine but UNRELATED 2-node chains → 2 separate chains, never
  linked (the cardinal rule with non-trivial chains on both sides).
- `healthy-no-degradation` — no degraded node → 0 chains, never invented.

## Machinery

- **Core (doc 15 cap. B):** `obsd/internal/flow/transitive.go` — `TransitiveChains` walks
  `NeighboursInto(EdgeTypeFlow,…)` transitively from the structural root (deepest degraded callee),
  deterministic (sorted collections, visited-set cycle terminator, `TransitiveMaxHops` ceiling).
  Extends `flow.Chain` with an ordered `Path []PathStep` + stated `Gaps []ChainGap` (both
  `omitempty`, so the one-hop `CrossServiceChain` output stays byte-identical — `xsvc-gate` still PASSES).
- **Direction** is taken ONLY from the authored relation (`RelationFromGraph`, the curated
  `PHEN_UPSTREAM_DEGRADATION → PHEN_DOWNSTREAM_IMPACT`). No relation ⇒ no chain (stated). The
  released graph hash is UNCHANGED (B adds NO graph content — it reuses the curated relation).
- Regen: `REGEN_TRANSITIVE_CORPUS=1 go test ./obsd/internal/flow -run RegenTransitiveCorpus`.
- Always-on Go guards: `TestTransitiveCorpusFrozenConsistent` (frozen-corpus determinism) + 8
  `Transitive*` unit tests (linear chain, silent-intermediate-not-bridged, the cardinal no-false-
  chain, fan-in, cycle-terminates, maxHops ceiling, no-relation-no-chain, determinism).
- Scorer: `harness/src/harness/transitive_chain_gate.py`; regression tests
  `harness/tests/test_transitive_chain_gate.py` (8 — good corpus passes + one mutation per floor,
  incl. two cardinal-false-chain mutations).
- Surface: `/api/root-cause-chain` (`api/rootcause.go`, `BuildRootCauseChain` — OFF / quiet / active
  states, charter-clean prose). Warm-path wiring in `cmd/obsd/main.go` reuses the SAME degraded set
  + authored relation as the one-hop lane; **strictly off-digest** (never perturbs the tick or replay digest).
- Recipe: `just transitive-chain-gate` (Go -race + Python).

## LIVE-VERIFIED on kind-vigil (2026-06-16)

`obsd --flow-enabled --app-metrics-enabled` + a synthetic 3-service chain in ns `traffic`
(`front → mid → back`, each calling the next via its ClusterIP Service so the conntrack-agent
records the observed-flow edges, each exposing `app_queue_depth 1500` over `slo.queue.max_depth 1000`
so each is a MEASURED degraded callee):

- **Flow + findings live**: the flow collector asserted 18 observed-flow edges; all three services
  fired `PHEN_APP_QUEUE_SATURATION` (3 active traffic findings); the one-hop `/api/cross-service`
  lane went active.
- **Transitive chain reconstructed** at `/api/root-cause-chain`: **ONE chain, ROOT `traffic/back`**
  (the deepest degraded callee), `hop1: back → mid [valid]`, `hop2: mid → front [valid]` — each hop
  a per-node MEASURED `PHEN_APP_QUEUE_SATURATION` + a MEASURED observed-flow edge `[valid]` + the
  verbatim AUTHORED relation why, each labelled (`class: MEASURED ⋈ AUTHORED (joined, never fused)`).
  The smart-traffic chain reaction, stitched live from real conntrack edges + real findings.
- **CARDINAL rule holds LIVE**: only the three flow-connected `traffic/*` services appear in the
  chain — none of the ~31 other cluster workloads (boutique, kube-system) leaked in. Independent
  faults are never falsely merged.
- **Charter live**: no causal token in the surface; the only "why" is the authored relation.
- **Non-gating**: `--flow-enabled` off ⇒ the lane reports its honest OFF state; the replay digest is
  unchanged (the warm-path chain is a pure consequence of the captured topology, off-digest).

## Honest scope

The chain asserts **adjacency + observed-flow + authored-relation + per-node measured degradation**
— NOT cause. Direction comes only from the authored relation (no human-authored direction ⇒ the
chain breaks and says so). Silent intermediates are stated gaps, never bridges. The orienting
relation is the GENERIC curated cross-service relation; per-phenomenon-pair relations (e.g.
"memory-pressure on a callee → queue-saturation on a caller") are a richer authoring follow-up,
same machinery. The next capability (D) forecasts this chain's ripple ahead of time.
