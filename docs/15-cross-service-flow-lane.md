# 15 — Cross-Service Flow Lane (v2)

**Status:** v2 design (branch `v2`). Extends the blueprint suite (00–14). The
hypothesis is VALIDATED by a live spike (`corpus/labels/flow-edge-spike.md`); this
document specifies how it becomes a production lane that integrates *seamlessly* with
the existing system — same epistemic charter, same determinism guarantee, same gates.

> **One-line scope.** Discover service→service dependency edges automatically by
> observing Linux conntrack (a MEASURED fact), and let the existing cascade +
> forecasting machinery trace interdependent failures across them — without the
> operator declaring any topology, and without weakening any guarantee.

---

## 1. Epistemic placement (doc 01 — unchanged discipline)

- **The flow edge is MEASURED.** "A repeatedly opened TCP connections to B" is an
  observed fact. It is surfaced as **"observed flow"**, never "depends-on"/"calls".
- **The cross-service "why" is AUTHORED.** One curated `phenomenon_relation`
  (callee-degradation → caller-impact), surfaced verbatim with author+version.
- **The "root" is a STRUCTURAL position** over the observed graph (most-upstream
  degraded node) — MEASURED + topological, never a causal claim. The tokens
  cause/caused/root-cause appear nowhere in the output.
- These three are **JOINED adjacently**, each labelled, **never fused**.

This is the same join the system already performs for leak→OOM (07 M5). The flow lane
adds a new MEASURED *edge type* and one new AUTHORED *relation* — no new class of
statement, no learned value, no model on the detection path.

---

## 2. What the spike proved (and what it deliberately deferred)

Live on Online Boutique (`corpus/labels/flow-edge-spike.md`):

| Criterion | Result |
|---|---|
| Dependency discovery (C1) | Precision/Recall/F1 = **1.000** (16/16 edges, decoy excluded) |
| Root-cause tracing (C2) | **4/4** trials name the degraded hub; 0 symptom-as-root; discriminator holds |
| Direction (C3) | all edges correctly oriented, 0 reply-tuple phantoms |
| Non-disruption (C4) | spike fully isolated; full repo suite green |
| Charter (C5) | MEASURED edge + 1 AUTHORED relation, no fused causation |

Deferred to this lane: a production collector (not `docker exec`), cross-node SNAT
caller recovery, and **wiring flow into the deterministic path while keeping replay
byte-identical** — Phase B, below, now PROVEN feasible.

---

## 3. Phase B — flow edges in the deterministic core (the keystone, de-risked)

**Finding (from reading the replay machinery): the determinism mechanism flow needs
already exists.** The architecture was built (doc 07 §3.7) so that "same readings +
same graph version + same **topology snapshot** ⇒ same matches." That topology
snapshot is captured per-tick and replayed. Flow edges are *just another edge type*
in it.

### 3.1 The chain of existing mechanism flow rides

1. **`identity.EdgeStore`** already holds arbitrary `EdgeType` strings and answers the
   validity-intersection traversal contract. Flow edges (`EdgeType("flow")`) are
   asserted with timestamps like any other edge.
2. **`EdgeStore.Snapshot()`** ([edges_snapshot.go](../obsd/internal/identity/edges_snapshot.go))
   emits every resident assertion as `EdgeSnap{Type,From,To,AssertedAt,LastConfirmed,
   RetractedAt}`, canonically sorted by `(type,from,to)`. Flow edges appear with
   `Type:"flow"` — no change needed.
3. **`replay.TickRecord.Topology []EdgeSnap`** ([digest.go:70](../obsd/internal/replay/digest.go))
   captures that snapshot **per tick** — already the input the matcher walks.
4. **`replay.Manifest.EdgeBudgets`** ([capture.go:43](../obsd/internal/replay/capture.go))
   pins per-edge-type staleness budgets; suspicion is budget-relative, so this is
   digest-bearing. The flow budget is pinned here.
5. **`identity.NewEdgeStoreFromSnapshot(snap, budgets)`** rebuilds the traversable
   store on replay ([engine.go:306](../obsd/internal/replay/engine.go)); the matcher
   walks the rebuilt store. Flow edges reconstruct identically under the pinned budget.

### 3.2 Proven in a safe testing environment

`obsd/internal/flow/determinism_test.go` (`TestCaptureReplayDeterminism`, network-free,
`-race`) demonstrates the round-trip on the real boutique edges:

```
EdgeStore.Snapshot()  →  []EdgeSnap (JSON on disk)  →  NewEdgeStoreFromSnapshot()
```
- the re-snapshot is **byte-identical** to the live snapshot, and
- `StructuralCascade` (the digest-bearing core) is **identical** live vs replayed
  (root = productcatalog, 3 impacted, all edges `valid`).

`obsd/internal/flow/structural.go` isolates that digest-bearing core: `StructuralCascade`
is a pure function of `(store, degraded set, window)`. `Walk` now derives its structure
from it and only *decorates* with surfacing metadata — so the operator-facing chain can
never disagree with what a replay reproduces.

### 3.3 The digest-bearing / off-digest split (the robustness rule)

| Carried where | What | In the digest? |
|---|---|---|
| `EdgeSnap` (captured) | flow edge existence + validity (`type,from,to,timestamps`) | **YES** — drives matching/cascades |
| warm side-channel | flow metadata (service ports, conn-depth, VIP breadcrumb) | **NO** — surfacing only, like forecast bands |

The matcher's cross-service decision uses only existence+validity (in `EdgeSnap`).
Metadata that is not needed to decide a match stays off the digest — so adding richer
flow telemetry later never threatens byte-identity.

### 3.4 Code changes Phase B actually requires (small + bounded)

1. **`obsd/internal/params`**: add a `flow` staleness budget to the edge-budget map
   (long-lived gRPC channels → a generous budget). **MUST stay OUT of
   `requiredEdgeBudgets`** (the fixed 5: runs-on/selects/mounts/owns/node-lease) — a
   flow-less cluster must validate fine. Flow is an **optional** edge type.
2. **`obsd/cmd/obsd`**: the collector (Phase A) asserts flow edges into the production
   `EdgeStore` on its own goroutine, under the same store lock the scrape path uses;
   the eval tick already snapshots the store under the gate. No tick-path change.
3. **`replay.Manifest`**: the flow budget flows into `EdgeBudgets` when the lane is on;
   add a `"flow"` entry to `Contents` (and to `Absent` when off) — honest coverage.
4. **No change to** `replay/digest.go`, `replay/engine.go`, or
   `identity/edges_snapshot.go` — they are already edge-type-generic. (Verified.)

### 3.5 Backward compatibility & failure modes (designed-in)

- **Existing bundles replay unchanged**: no flow edges in their `Topology`, flow absent
  from their `EdgeBudgets` — the engine's `TopologyLess`/budget-pin handling already
  covers "this regime didn't have edge type X."
- **Collector down / lane off**: zero flow edges asserted ⇒ the deterministic tick is
  byte-identical to today. **Non-gating is preserved**: detection never waits on the
  collector.
- **Stale flow edge**: the validity contract degrades it to `suspect` (budget-relative),
  exactly like a stale `runs-on` — never a fabricated co-occurrence.

---

## 4. Revised phase plan (A–F) — what each adds, where it touches, its gate

### Phase A — Production collector
- **What:** a privileged DaemonSet (one/node) reading `/proc/net/nf_conntrack` (v1) →
  eBPF socket/conntrack tracing (v2, recovers SNAT-masked cross-node callers). Parses +
  DNAT-recovers (reply-tuple rule, proven), maps IP→CEI via the identity informer,
  emits flow observations to obsd.
- **Touches:** new `deploy/` DaemonSet + RBAC; reuses `internal/flow/conntrack.go` +
  `recover.go` + `resolver.go` (already built & tested).
- **Determinism note (revised):** the collector is a *live-only input*. Replay needs
  only the resulting **edge snapshot** (captured in `TickRecord.Topology`), not raw
  conntrack — so the collector needs **no bundle format of its own**. (Optionally
  capture raw conntrack as a `Contents` addition for debugging/re-derivation; not
  required for determinism.)
- **Gate:** the C1 graph-accuracy bar on a live cluster (precision ≥0.9, recall ≥
  measured ceiling), coverage honestly reported.

### Phase B — Identity/replay integration *(this document; determinism PROVEN)*
- **What:** §3.4 changes — flow into the production EdgeStore + budget pin + manifest
  contents. The `StructuralCascade` core is the matcher's cross-service decision.
- **Gate:** a determinism gate in the harness — capture a bundle with flow edges on a
  live cluster, replay, assert **digests byte-identical** (the existing replay gate,
  now exercised with flow edges present).

### Phase C — Governance (the AUTHORED relation)
- **What:** promote `overlays/experimental/flow-relation-v0.yaml` to a real ontology
  release: author `PHEN_UPSTREAM_DEGRADATION` / `PHEN_DOWNSTREAM_IMPACT` (or bind to
  existing phenomena) as a directed `phenomenon_relation` with temporal tags; add
  `"flow"` to `graph.knownTraversalEdgeTypes` ([overlay.go:60](../obsd/internal/graph/overlay.go))
  for cross-service spans; bind `PHEN_DOWNSTREAM_IMPACT` to a **measured** signal
  (caller-side conn-depth / latency rise) so both ends are MEASURED.
- **Process:** `govern classify` (expect behavioural-medium) → release `v0.4.0` →
  re-bind → harness regression gates. **Not a casual edit** (doc 12).

### Phase D — Detection integration (directed cascade)
- **What:** the production cascade walk gains **flow-direction-awareness**. Today
  `detect.related()` walks `{runs-on,mounts,selects}` *symmetrically*; flow impact is
  **directed** (propagates callee→caller, against the call arrow). Productionize
  `StructuralCascade`'s directed reverse-walk into `detect` so cross-service cascades +
  blast radius fire natively.
- **Touches:** `obsd/internal/detect/cascade.go` (directed traversal for the flow span);
  the span definition declares it walks `flow` in the cause direction.
- **Gate:** **backtest gate before operator-visible** (the gate rule, doc 11 §3.5):
  a cross-service-cascade corpus (the boutique fan-in scenarios) + confirm/refute
  calibration. Until it passes, the class is computed but not surfaced.

### Phase E — Forecasting integration (the payoff: anticipatory cascade)
- **What:** the anticipatory-cascade hypothesis (the earlier design) becomes feasible
  because flow edges now exist in the topology the forecast lane can walk: a forecast on
  a precursor propagates along flow edges + the authored relation to a **conditional
  downstream hypothesis** with confirm/refute. Clock stays blind (bare floats); the
  join happens at the surface.
- **Touches:** `obsd/internal/forecast` (propagate the projected crossing along the
  flow span) + the warm-path surface; **off the deterministic digest** (warm path).
- **Gate:** its own backtest gate (lead-time + confirm/refute calibration).

### Phase F — Surfacing
- **What:** render flow edges in `TopologyView` (new edge type, "observed flow" label,
  validity/confidence marks); the traced chain in `InsightFeed` (three classes
  labelled); flow coverage in the Coverage report (resolvable/snat-masked/unresolved as
  honest partial coverage).
- **Touches:** `obsd/internal/api` + `web/src/surfaces`.
- **Gate:** the charter battery extended to the flow surfaces (no dependency-verbs, no
  fused causation).

---

## 5. Other-phase revisions to SUPPORT this scope (existing system)

The integration is unusually clean because the original architecture already does the
hard part (per-tick typed topology capture + snapshot round-trip + budget pinning).
The only existing-system touchpoints:

| Component | Change | Risk |
|---|---|---|
| `internal/params` | add optional `flow` budget; keep `requiredEdgeBudgets` at 5 | low |
| `internal/graph` (overlay) | `flow` in `knownTraversalEdgeTypes` (Phase C) | low |
| `internal/detect` | directed flow-awareness in the cascade walk (Phase D) | **medium — the one real detection-path change; gated** |
| `internal/replay` | none (generic) — **verified** | none |
| `internal/identity` | none (EdgeStore + snapshot already generic) — **verified** | none |
| `cmd/obsd` | wire the collector goroutine (Phase A/B) | low |

---

## 6. Consolidated breakdown — what we will develop, in order

```
A. Collector            privileged DaemonSet: conntrack→flow observations→obsd
                        (reuses internal/flow parse/DNAT/resolver, already built+tested)
                        gate: live C1 graph accuracy
        │
B. Determinism wiring   flow edges into the production EdgeStore; budget pin; manifest
   (PROVEN)             contents. StructuralCascade = the matcher's cross-service core.
                        gate: replay byte-identical with flow edges present
        │
C. Governance           author the directed phenomenon_relation + flow traversal vocab;
                        bind downstream to a measured signal. classify→release v0.4.0→re-bind.
        │
D. Detection            directed cross-service cascade + blast radius in detect.
                        gate: cross-service-cascade backtest before operator-visible
        │
E. Forecasting          anticipatory cascade: propagate the clock along flow edges →
                        conditional downstream hypothesis + confirm/refute (warm path).
                        gate: lead-time + calibration backtest
        │
F. Surfacing            TopologyView flow edges + InsightFeed chain + coverage; charter battery.
```

**Sequencing rule:** A→B unlock everything (no live edges, no determinism = nothing
downstream). C gates the ontology change. D/E each carry their own backtest gate before
becoming operator-visible — the system computes them dark until calibrated, exactly like
the working-set forecast class. Nothing surfaces a cross-service "story" to an operator
before it has proven its calibration.

**The two real risks, both contained:** (1) the directed cascade in `detect` (Phase D)
is the only change to the deterministic detection path — it is gated and replay-verified;
(2) cross-node SNAT recovery (Phase A v2/eBPF) — until then, the recoverable subgraph is
stated as honest partial coverage, never guessed.

**What stays invariant:** the three provenance classes; no learned value anywhere;
detection never waits on the collector or the clock (non-gating); bars only from declared
config; every (entity,variable) and every dependency edge carries a visible
observed/declared/unknown state.
