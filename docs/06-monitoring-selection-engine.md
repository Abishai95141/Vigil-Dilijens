# 06 — Monitoring Selection Engine

**Role in suite:** The attention allocator. A cluster holds far more entities than can be watched equally, and clock inference on every eligible gauge of every entity is infeasible. Selection decides which entities are monitored, with which neighbourhoods, at which intensity — driven by the graph rather than by guesswork — and keeps cost bounded by design.

---

## 1. Purpose and ownership

This document owns: the selection funnel; topological neighbourhood expansion; the intensity tiers and their cost model; criticality and role weighting; operator scope overrides; reason codes; and re-selection reactivity.

It does not own: coverage facts (04/05), the phenomena that define monitoring-worthiness (02), what detection does with the selected set (07), or which signals on selected entities are forecastable (09 owns the signal-level funnel; this engine owns the entity level).

## 2. System context

The same artifact that defines meaning defines attention: phenomena encode what is diagnostically meaningful, so phenomenon participation — not popularity or guesswork — is what earns an entity monitoring. The selector turns the bound customer graph (04) into a watch list with intensities, and because variables instantiate from types, entities created after onboarding are auto-considered with zero new authoring.

## 3. Core mechanics

### 3.1 The selection funnel (per entity)

1. **Coverage gate.** Only entities passing scope + binding (05 §3.3) are candidates; an entity with no observable signals cannot be monitored regardless of importance — and its exclusion is a visible coverage fact, not a silent drop.
2. **Phenomenon participation.** An entity is worth *detecting* on if it carries member signals of at least one phenomenon, read straight from the graph.
3. **Forecast eligibility (Tier-B narrowing).** For the forecasting overlay only: narrow to entities carrying gauge-typed, regularly sampled, dynamics-showing signals per the 09 funnel, prioritizing `T0-` precursors — only a precursor's crossing carries early-warning meaning.
4. **Criticality and role weighting.** Weight up stateful workloads, single-replica roles, and nodes (a node's trouble has wide blast radius); weight down ephemeral or redundant entities. Weighting at the *role* layer (03) keeps it stable across instance churn.
5. **Operator scope.** Operators pin namespaces or entities in-focus or mark them out-of-scope; explicit operator scope overrides the automatic result and is recorded as the reason.

### 3.2 Neighbourhood expansion

Selection is topological, not per-entity-in-isolation. Selecting an entity for a phenomenon whose span is first- or second-order implies selecting the **neighbourhood that phenomenon needs**: a node-pressure pattern cannot be evaluated without the signals of the pods that run on that node. The selector expands each chosen entity out to the spans of the phenomena it participates in, along the declared traversal edge types, honouring edge validity (03 §3.5), and includes the neighbours' relevant member signals in the watch set.

### 3.3 Intensity tiers — the cost model

| Tier | What runs | Cost character | Population |
|---|---|---|---|
| **A — detection** | The three-primitive checks and fingerprints (05), feeding phenomenon matching (07) | Arithmetic; scales to the whole eligible cluster | Every coverage-passing, phenomenon-participating entity plus required neighbourhoods |
| **B — forecasting** | The clock pipeline (09) on selected precursor signals | Model inference; deliberately scarce | Precursors that matter on entities that matter — the intersection of this funnel with 09's signal funnel |
| **none** | Nothing beyond ingest | — | Out-of-scope or coverage-failing entities, listed with reasons |

Tier B is budgeted: the number of clock invocations per cycle is a configured ceiling, and the weighting of §3.1 ranks candidates within it. Cost is bounded by construction, not by hope.

### 3.4 Re-selection

The selected set recomputes on triggers: binding change (new or lost signals, from 04 M6), topology change (entities created, destroyed, rewired — new entities auto-enter consideration), and operator scope change. Re-selection is incremental where possible and always publishes a diff (what entered, what left, why), so attention changes are auditable.

### 3.5 Conceptual structure: monitoring selection record

| Field | Meaning |
|---|---|
| Entity CEI | Instance or role being selected |
| Passes coverage | Gate 1 result |
| Participating phenomena | Which phenomena earned it attention |
| Neighbourhood CEIs | Entities pulled in by span expansion, with the edge paths used |
| Tier | A-detection / B-forecasting / none |
| Reason code | phenomenon-member / precursor / critical-role / operator-pinned / out-of-scope |
| Selected-at; trigger | Audit trail of why the set looks as it does |

## 4. Epistemic discipline enforcement

Selection records are MEASURED-class facts about the system's own attention, with reasons attached — the operator can always answer "why is this watched and that not." Selection never manufactures meaning: it only routes entities toward checks whose semantics live in the graph. Operator pins are recorded as operator-sourced, distinct from graph-derived reasons.

## 5. Structural dependencies

**Consumes:** phenomena and criticality-relevant typing (02); validity-aware topology and role identity (03); bindings and coverage (04/05). **Provides:** the Tier-A set plus neighbourhoods to detection (07); the Tier-B entity set to forecasting (09); scope state to the configuration surface (10). **Reacts to:** re-binding (04) and operator actions (10).

## 6. Failure modes and honesty mechanisms

Important-but-unselected entities — the inherent risk of selection; countered by the criticality weights, the operator pin override, the published none-list with reasons, and the unexplained channel (08) still observing Tier-A breadth. Tier-B starvation under tight budgets — countered by ranked candidates and a visible "eligible but unbudgeted" list rather than silent omission. Thrash under churny clusters — countered by role-layer weighting and damped re-selection (diffs over rebuilds).

## 7. R&D execution sequence

1. **M1 — Funnel gates 1–2** (Phase 0b). Coverage gate + phenomenon participation over the bound customer graph. Exit: selection set with reason codes on reference clusters.
2. **M2 — Neighbourhood closure** (Phase 1 entry). Span expansion along declared edges with validity intersection. Exit: every spanned phenomenon's required neighbourhood is present in the watch set or its absence is a recorded coverage gap.
3. **M3 — Tiering and reason codes** (Phase 0b–1). Tier A live first; Tier B structure in place, empty until Phase 2. Exit: tier assignment auditable end to end.
4. **M4 — Criticality weighting + operator scope** (Phase 1). Exit: pins and exclusions override and record correctly.
5. **M5 — Tier-B budgeting** (Phase 2, with 09). Exit: clock invocations respect the ceiling; unbudgeted-eligible list published.
6. **M6 — Re-selection reactivity** (Phase 1–2). Exit: binding/topology/operator triggers produce correct, diffed updates within the staleness budget.

## 8. Open questions owned here

How aggressive the criticality weighting and the Tier cutoffs should be, and how much of that tuning is exposed to the operator versus held as policy; whether Tier-B budget should be global or per-namespace; damping constants for re-selection under high churn — all to be set from harness evidence (11), not intuition.
