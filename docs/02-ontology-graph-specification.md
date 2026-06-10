# 02 — Ontology Graph Specification

**Role in suite:** Pillar 1. The single curated, versioned, type-level knowledge artifact that every other component reads. Written and changed only through Governance (12); validated continuously by the Harness (11).

---

## 1. Purpose and ownership

This document owns the conceptual schema and authoring invariants of the ontology graph: what node and edge families exist, what every field means, and what a curator is allowed and required to assert. It owns the type level only.

It explicitly does not own: instance-level content (the bound customer graph belongs to 04), identity mechanics (03), runtime evaluation of anything it describes (05/07/09), or the change process (12).

## 2. System context

The system's intelligence is a curated graph plus a pretrained clock. The graph holds four kinds of knowledge — what a cluster can emit, how entities are wired, which multi-signal phenomena matter, and where each signal's limit comes from — and it is the *only* source of meaning in the product. Detection reads its phenomena to answer "what is happening now"; forecasting reads its signals and precursor edges to answer "what crosses soon, and what does that precede." Reference scale at blueprint time: 842 nodes, 3,759 edges, 589 signals (Metric 457, Log 40, State 35, Event 33, Trace 14, Profile 8, Audit 2; by data type: Counter 190, Gauge 151, Histogram 22, plus structs, event records, and text). One governing fact for the whole architecture: **only the 457 Metric-modality signals are numeric time series**; the remaining 132 participate in detection but are never forecastable.

## 3. Core mechanics

### 3.1 Node families

**Signals (variables).** Every observation a cluster can emit, with the metadata needed to find it, interpret it, and generalize it. A signal is the unit of authorship; instances are generated, never hand-written.

**Entity types.** Pods, nodes, containers, volumes, services, and the rest of the Kubernetes object families, at the *type* level, including the instance/role identity distinction each type supports (a physical `PodInstance` dies and is replaced; the logical `PodRole` it plays is stable across restarts).

**Equivalence groups.** The bridge across customer naming dialects: a group carries one canonical name plus the known variants exporters use for the same quantity. Group membership is a semantic claim — same quantity, same units after normalization, same instantaneous-vs-cumulative character — and is therefore subject to the binding QA of 04/11.

**Phenomena.** Named multi-signal patterns — the failure knowledge of the product. Each phenomenon declares its member signals, each tagged with a temporal role and the entity role that carries it; a topological span; the edge types its members are distributed across; and relation edges to other phenomena.

### 3.2 Edge families

**Topology edge types** (`runs-on`, `mounts`, `selects`, and peers): the relationship vocabulary that instance-level topology will use and that detection traverses. The ontology defines the types and their semantics; instances and their timestamps belong to 03.

**Phenomenon membership edges**: signal → phenomenon, carrying the member tags (below).

**Phenomenon relation edges**: phenomenon → phenomenon, with role `trigger` or `downstream` and an authored note. These edges are the sole source of cascades and blast radius.

**Threshold-sourcing edges**: signal → threshold rule.

### 3.3 Conceptual structure: Variable

| Field | Meaning | Constraints |
|---|---|---|
| Canonical id | Stable identifier and canonical name | Unique; never reused |
| Modality | Metric, Log, State, Event, Trace, Profile, Audit | Only Metric is a numeric series |
| Data type | Gauge, counter, histogram, struct, event, text | Forecast eligibility derives from this (09) |
| Update cadence class | Scrape (typically 15–30 s), watch, event-driven, streaming | Regular cadence is a forecast prerequisite |
| Equivalence group | Reference, or none if the name is universal | Membership is a falsifiable semantic claim |
| Expected entity scope | The entity *types* this variable applies to | Drives instance fan-out in 04 |
| Identity scope | Instance, role, or either | Drives the two binding modes of 04 |
| Capability prerequisites | What the cluster must support for this signal to exist (kernel features, components) | Absent capability ⇒ signal does not exist for that customer |
| Distro/version gates | Platform or version variations in the signal's behaviour or semantics | Includes semantics shifts such as cgroup v1 vs v2 |
| Threshold rule | Reference to where this signal's bar comes from | May be absent for purely diagnostic signals |

### 3.4 Conceptual structure: Threshold rule

| Field | Meaning | Constraints |
|---|---|---|
| Kind | Absolute threshold, config-relative threshold, rate-of-change guard, or co-occurrence participation | The complete vocabulary of checks; nothing else exists |
| Source precedence | Customer Kubernetes config → operator override → ontology default | Strict order, resolved per instance at evaluation time |
| Config path | Where in the customer's spec the bar is read (e.g. the container memory limit) | Per-instance resolution; the primary generalization mechanism |
| Relative factor | Multiplier on the config value (e.g. limit × 0.95) | — |
| Default value | Fallback used only when neither config nor override applies | Must be flagged as a default wherever surfaced; defaults are high-blast-radius content under 12 |
| Window | Evaluation window for rate and co-occurrence kinds | — |

"Dynamic to the customer's system" means **reads their configuration**, never **learns their behaviour**.

### 3.5 Conceptual structure: Phenomenon

| Field | Meaning | Constraints |
|---|---|---|
| Id and name | Stable identifier, human-facing name | — |
| Topological span | Entity-local, first-order, or second-order | Declares how far detection walks |
| Traversal edge types | Which topology edge types the members are distributed across | Walks follow only declared types |
| Members | Each: variable reference; the entity role in the span that carries it; temporal order tag `T0-` (precursor), `T0` (event), `T0+` (consequence); role `required` or `supporting`; an authored human-facing note | Temporal tags are empirical claims, falsified by 11 |
| Relations | Each: target phenomenon; role `trigger` or `downstream`; authored note | Sole source of cascades and blast radius |

Worked example, the OOM pattern: working-set memory at `T0-` ("approaches limit pre-OOM"), the OOM kill event at `T0`, restart and last-state evidence at `T0+`; span entity-local; a relation records that a memory-leak phenomenon is its upstream `trigger` and that an OOM on a pod stresses its node (first-order) and can evict siblings (second-order).

### 3.6 Authoring invariants

Every reason note is authored prose with author and version provenance — no generated text may enter the graph. Every phenomenon declares its span and traversal edges explicitly; an undeclared span is invalid, not "entity-local by default." Default thresholds are flagged. Temporal tags and spans are treated as falsifiable claims and carry links to the evidence (incident classes, references) the curator relied on, so the harness can target them. The graph holds no instances, no measurements, no learned values, and no model outputs — it is pure type-level assertion.

## 4. Epistemic discipline enforcement

Everything in this artifact is AUTHORED-class by definition. The graph is the only legal source of the system's "reasons," and Governance (12) is its only write path. The schema makes fusion impossible at the root: there is no field anywhere in which a measurement or a model output could be stored.

## 5. Structural dependencies

**Consumes:** the charter (01) as normative input. **Provides:** the knowledge read by binding (04), the bar vocabulary used by observation (05), the participation and criticality inputs to selection (06), the detection units and relation edges for detection (07), the precursor tags and bars for forecasting (09), the reason notes rendered by surfacing (10), and the falsification targets for the harness (11). Changed only via 12.

## 6. Failure modes and honesty mechanisms

A wrong equivalence membership corrupts a customer silently — mitigated by binding QA (04/11) and validation status on bindings. A wrong temporal tag or span causes systematic misses or false stories — mitigated by the falsification suite (11). A bad default threshold propagates to every customer on the fallback — mitigated by default flagging and high-rigor review class (12). Coverage of the signal catalogue itself is finite and versioned; what the graph does not describe, the system cannot detect, which is why the unexplained channel (08) exists.

## 7. R&D execution sequence

1. **M1 — Schema v1** (Phase 0a). Freeze the conceptual structures above as the authoring contract. Exit: schema review signed off against the charter.
2. **M2 — Reference graph encoding** (Phase 0a–0b). Encode the existing 842-node reference graph into schema v1, including span and traversal-edge declarations for every phenomenon. Exit: zero invariant violations under authoring lints.
3. **M3 — Authoring lints** (Phase 0b). Mechanical checks for the invariants of §3.6. Exit: lints wired into the governance review path (12).
4. **M4 — Evidence linkage** (Phase 1). Every phenomenon's temporal tags carry evidence references consumable by the harness. Exit: 11's falsification suite can enumerate its targets from the graph alone.
5. **M5 — Catalogue growth loop** (continuous). New signals, groups, and phenomena enter only through 12, fed by 08's candidate patterns and 04's coverage gaps.

## 8. Open questions owned here

Whether histogram signals should carry per-quantile sub-variables at the type level or be derived at binding time (interacts with the forecast funnel in 09); how rich the entity-type catalogue must be beyond the core families before Phase 1 (services and network policies are mostly structural, with little instrumentation); the right granularity for distro gates so they stay maintainable across the supported platform matrix.
