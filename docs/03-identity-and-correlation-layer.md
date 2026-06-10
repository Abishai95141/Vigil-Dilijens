# 03 — Identity & Correlation Layer

**Role in suite:** Prerequisite zero. The referential foundation that joins the knowledge world (graph nodes) to the measurement world (time-series streams), and that keeps topology truthful in time. Every downstream behaviour — binding, detection, forecasting — is only as correct as this layer.

---

## 1. Purpose and ownership

This document owns entity identity end to end: the canonical identity that names an entity once for both stores; the two-layer identity model (mortal instance, durable role); label normalization from heterogeneous exporters into canonical identity; the identity lifecycle under Kubernetes churn; the timestamped-edge model that gives topology a validity dimension; and the exact-match join contract between graph and store.

It does not own: what entities mean (02), what gets bound to them (04), or what is computed on their series (05+). It owns *which thing is which, and when*.

## 2. System context

The ontology graph and the Quantitative State Store are physically separate by design: the graph answers *what is this, what is it wired to, where is its bar*; the store answers *what are its numbers doing*. Neither is useful alone, and they are logically joined only by identity keys. In a real cluster this join is the hardest part of the whole system: different exporter families label the same container differently; pods churn between UID, name, and logical role; rescheduling rewires topology continuously. If the join is wrong, detection correlates the wrong entities and forecasts project the wrong series against the wrong bars — and both failures are silent. That is why this layer is built first and gated hardest.

## 3. Core mechanics

### 3.1 The two-layer identity model

| Layer | Nature | Lifetime | Typical question it serves |
|---|---|---|---|
| **Entity instance** | The physical object (this pod, UID-anchored) | Mortal: born, lives, dies, is replaced | "Is *this* pod leaking right now?" |
| **Entity role** | The logical function (the `currencyservice` workload member) | Durable across restarts and rescheduling | "Does `currencyservice` OOM every few hours, whichever instance is current?" |

Role derivation follows the cluster's own ownership chain (instance → owning workload → role key within namespace and cluster). Every instance carries exactly one role; a role carries a succession of instances over time. Variables in the ontology declare which layer they bind to (02 §3.3); both layers are first-class join targets.

### 3.2 Canonical Entity Identity (CEI)

A single normalized identity key is minted at discovery for every instance and every role, built from stable coordinates (cluster, namespace, kind, name, UID for instances; cluster, namespace, kind, role key for roles). The CEI is the only join key in the system:

- Every graph instance node carries its CEI.
- Every time-series stream carries its CEI, stamped at ingest.
- The graph↔store join is **exact match on CEI, never fuzzy, never at read time**. All reconciliation happens at ingest and discovery; the hot path performs lookups only.

### 3.3 Label normalization

Each exporter family labels telemetry in its own dialect (the container-runtime exporter, the cluster-state exporter, the OpenTelemetry semantic conventions, and customer-specific relabeling on top). The layer maintains a **normalization map per exporter family**: declarative rules that translate a source label set into CEI coordinates. Normalization is the single point where dialects collapse; downstream components never see raw labels. Streams whose labels cannot be normalized to a known CEI are **quarantined as unjoinable** — stored, counted, surfaced in the coverage report (04), and never guessed into an identity.

### 3.4 Identity lifecycle

The layer runs a lifecycle state machine per instance: discovered → active → terminated (tombstoned), with **succession** recorded when a new instance is born under an existing role. Rules: a restart is a new instance under the same role; rescheduling moves an instance's topology edges, not its identity; role continuity survives any instance event; tombstones are retained long enough that late-arriving samples join the correct, now-dead instance rather than being orphaned or mis-joined.

### 3.5 Timestamped topology: edges as assertions with validity

Topology edges (`runs-on`, `mounts`, `selects`, per the type vocabulary of 02) are recorded as **timestamped assertions with validity intervals**: asserted-at, last-confirmed-at, retracted-at. Two consequences govern the rest of the system:

- **Staleness budgets per edge type.** Each edge type carries a maximum tolerated confirmation age; an edge past budget is *suspect* and is reported as such rather than silently trusted.
- **Walks intersect validity with the evaluation window.** Any consumer that traverses topology (detection's spans and cascades in 07, selection's neighbourhood expansion in 06, blast-radius derivation) must require edge validity to overlap the time window being evaluated. A co-occurrence "across" an edge that was not valid during the window is not a co-occurrence. This single rule is what prevents the worst trust failure available to the product: fabricating a 2-hop correlation through a stale edge.

### 3.6 Conceptual structures

| Structure | Fields (meaning) |
|---|---|
| Canonical Entity Identity | Layer (instance/role); cluster; namespace; kind; name; UID or role key; minted-at |
| Identity lifecycle record | CEI; state (discovered/active/terminated); born-at; died-at; role CEI; predecessor/successor instance CEIs |
| Topology edge assertion | Edge type; from-CEI; to-CEI; asserted-at; last-confirmed-at; retracted-at; staleness status |
| Normalization rule | Exporter family; source label pattern; CEI coordinate mapping; precedence |
| Join audit record | Stream key; resolved CEI or quarantine reason; ingest timestamp |

## 4. Epistemic discipline enforcement

Identity facts and edge assertions are MEASURED-class: they are read from the cluster's own control plane and telemetry, never inferred. Quarantine rather than guessing is this layer's expression of the honesty commitment — an unjoinable stream becomes a visible coverage fact, not a silent best-effort match. Suspect-edge reporting types the system's own topological uncertainty.

## 5. Structural dependencies

**Consumes:** the entity-type and edge-type vocabulary of 02; the charter (01). **Provides:** CEIs and lifecycle to binding (04, which instantiates variables onto identities), stream identity to observation (05), validity-aware topology to selection (06) and detection (07), and stable target identity to forecasting (09). **Gates:** Phase 0a cannot exit without this layer's accuracy gate.

## 6. Failure modes and honesty mechanisms

Mis-join (wrong CEI on a stream) — the silent killer; countered by exact-match discipline, per-family normalization tests, and continuous join audits sampling streams against control-plane truth. Orphaned series — counted, surfaced, never guessed. Identity flapping under rapid churn — countered by the lifecycle state machine and succession records; role-layer bindings absorb instance churn by construction. Stale edges — countered by validity intersection and staleness budgets; a suspect edge degrades a topological match (07) instead of inflating it. Exporter dialect drift after upgrades — countered by versioned normalization maps regression-tested in the harness (11).

**Layer health metrics (published, not internal):** join accuracy on sampled audits; orphaned-stream rate; edge staleness distribution per type; quarantine volume and reasons.

## 7. R&D execution sequence

1. **M1 — Identity model and CEI scheme** (Phase 0a). Define both layers, coordinates, and minting rules. Exit: CEI scheme review against churn scenarios (restart, reschedule, scale, recreate-with-same-name).
2. **M2 — Normalization maps for the core exporter families** (Phase 0a). Container-runtime, cluster-state, and OTel-semconv dialects first. Exit: every stream from reference clusters resolves or quarantines with a stated reason.
3. **M3 — Lifecycle state machine and succession** (Phase 0a). Exit: replayed churn scenarios produce correct instance/role continuity with zero orphan mis-joins.
4. **M4 — Timestamped edges and validity semantics** (Phase 0a–0b). Edge assertion store, staleness budgets per type, validity-intersection rule published as the traversal contract for 06/07. Exit: stale-edge scenarios degrade rather than fabricate matches in replay.
5. **M5 — Join audit tooling and health metrics** (Phase 0b, then continuous). Exit gate for Phase 0a overall: **join accuracy ≥ target on reference clusters, with all misses explained as quarantines, not mis-joins.**

## 8. Open questions owned here

Tombstone retention horizon versus late-sample arrival distributions; whether role keys need customer-tunable derivation for nonstandard ownership patterns (operators, CRD-managed workloads); per-edge-type staleness budget defaults, to be set empirically from reference-cluster churn data rather than intuition.
