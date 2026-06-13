# Phase B — flow edges in obsd's deterministic core — live-verified 2026-06-13

Phase B wires the flow collector into obsd: the agent's conntrack (Phase A) becomes
**observed-flow edges asserted into the same production `identity.EdgeStore` the eval
tick snapshots**, captured per-tick in `TickRecord.Topology` and pinned in the replay
manifest's `EdgeBudgets`. Behind `--flow-enabled` (OFF by default).

## Why it is determinism-safe (and proven so)
The per-tick digest is over `{fingerprints, findings, cascades, unexplained}` — **not
topology** (`replay/digest.go`). Flow edges enter `TickRecord.Topology` (for replay
rebuild) but the matcher has no flow span yet (that is Phase C/D), so they contribute
nothing to the digest. Therefore a flow-on bundle replays byte-identically, and obsd
with flow OFF is byte-identical to before. Flow rides the existing topology-capture
machinery (`EdgeStore.Snapshot` → `TickRecord.Topology` → `Manifest.EdgeBudgets` →
`NewEdgeStoreFromSnapshot`); no replay/identity code changed.

## What was built (branch `v2`)
- `obsd/cmd/obsd/flowcollect.go` — the collector goroutine: fetch conntrack per node via
  the API-server node proxy (`kube.ProxyFetcher`, the cAdvisor/node-exporter path),
  build workload→workload flow edges (`internal/flow`), assert them as `EdgeType("flow")`
  into the production EdgeStore **under the store gate** (so a tick reads a whole flow
  update, never a partial one).
- `obsd/cmd/obsd/main.go` — `--flow-enabled` / `--flow-interval`; the flow budget injected
  into the runtime budget map AND the manifest's `EdgeBudgets` (NOT into
  `requiredEdgeBudgets` — flow is optional); collector started only when enabled.

## Live verification (boutique)
- **Flow OFF:** capture → replay → "all 4 ticks replayed byte-identically" (unchanged obsd).
- **Flow ON:** the collector asserted **16 observed-flow edges per tick** (resolvable ~36,
  snat_masked ~200, unresolved 1 — the full boutique graph, live, inside obsd). The
  manifest's `EdgeBudgets` now carries **`flow`** alongside runs-on/selects/mounts/owns/
  node-lease. Capture → replay → **"all 5 ticks replayed byte-identically (doc 05 §3.5
  holds)"** — fingerprints=35, findings=0, identical to the flow-off structure (the matcher
  is unaffected by the presence of flow edges, as designed).
- Gate after: gofmt clean, `go build ./...` OK, `go vet` OK, race tests (flow/replay/
  identity) green. Boutique 12/12 healthy.

## State
Flow edges are now in the deterministic capture and replay-safe — the keystone. Next:
Phase C (governance: author the directed cross-service `phenomenon_relation` + add `flow`
to the traversal vocab + bind the downstream phenomenon to a measured signal) and Phase D
(the matcher walks flow edges directionally → cross-service cascades, behind a backtest
gate). Those are the steps that make the captured flow edges *do* something in detection.
