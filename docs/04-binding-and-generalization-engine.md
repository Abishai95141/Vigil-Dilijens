# 04 — Binding & Generalization Engine

**Role in suite:** The hidden third pillar: the compiler between the type-level ontology and a specific live cluster. Input: ontology graph (02) + identified cluster (03). Output: the bound customer graph — a generated, instance-level model with auto-calibrated bars — plus an honest coverage report. Runs at discovery time and on change, never in the hot path.

---

## 1. Purpose and ownership

This document owns the discovery-time compilation: capability gating, equivalence-group resolution with semantic validation, distro/version gating, instantiation of every variable across its three generalization axes, per-instance threshold resolution with resolvability accounting, the binding state model, and the coverage report. It owns the answer to "what, exactly, can this system see and check on *this* cluster, and where is its honesty about the rest."

It does not own: identity (03), what is computed on bound streams (05), which bound entities get watched (06), or the ontology content it compiles (02).

## 2. System context

The ontology is written once; clusters are heterogeneous and ever-changing. The product survives that contact through one central trick: **a variable is authored once and generalizes** — across customer metric names, across every entity of the right type, and across the two identity layers. This engine is where the trick executes. The compiler framing is exact: the ontology is source code, binding is compilation, the bound customer graph is the running program, and later runtime layers call into that program. Because instances are generated from types, entities that did not exist when the ontology was written are handled automatically: a new pod inherits every applicable variable and a bar from its own configuration, with zero new authoring.

## 3. Core mechanics

### 3.1 The four binding mechanisms (per signal, at discovery)

1. **Capability prerequisites.** Is this signal obtainable here at all (kernel features, optional components)? Absent capability ⇒ the signal does not exist for this customer; recorded as out-of-scope, not failure.
2. **Equivalence-group resolution.** Whatever the customer's exporter calls the quantity, it maps to the canonical variable through the group's known variants. This is the first generalization axis and the binding target.
3. **Emission metadata.** How and where to collect (endpoint, exporter, auth) — operational glue recorded per binding.
4. **Distro/version gates.** Same signal, different behaviour or semantics by platform or version; the gate selects the correct interpretation (including semantics shifts such as cgroup v1 versus v2 memory accounting).

### 3.2 Semantic equivalence validation (binding QA)

Name match is necessary, not sufficient. A false equivalence silently corrupts every downstream check for that customer, so each resolved binding passes semantic checks before it is trusted:

- **Unit normalization check** — the bound stream's units reconcile to the canonical variable's units.
- **Character check** — cumulative versus instantaneous matches the canonical data type (a counter masquerading as a gauge fails here).
- **Scope check** — the stream's entity granularity (container vs pod vs node) matches the variable's expected scope.
- **Platform-variant check** — distro gates confirm the semantics expected on this platform/version.
- **Range sanity** — observed values fall in physically plausible ranges for the quantity.

Each binding carries a **validation status**: verified / suspect / failed. Suspect bindings are usable but marked, surfaced in the coverage report, and prioritized for harness attention (11).

### 3.3 The three axes of instantiation

**Axis 1 — across names.** One canonical variable absorbs every customer dialect via its equivalence group, resolved once at binding time.

**Axis 2 — across entity instances.** A variable declares the entity *types* it applies to and is instantiated against **every** identified entity of those types. Its threshold rule, being config-relative, resolves **per instance** from that entity's own configuration. One authored line — working-set memory greater than the memory limit × 0.95 — becomes thousands of per-entity checks, each auto-calibrated: a pod with a 512 MiB limit gets a ~486 MiB bar; a pod with 4 GiB gets ~3.9 GiB; both from the same authored knowledge.

**Axis 3 — across identity layers.** Per the variable's identity scope, instantiation binds to the mortal instance ("is *this* pod leaking"), to the durable role ("does this service OOM every few hours, whichever instance is current"), or to both. Role bindings absorb instance churn by construction.

### 3.4 Threshold resolution and the resolvability hole

Resolution follows strict precedence per instance: **customer Kubernetes config → operator override → ontology default**, with defaults flagged wherever surfaced. The blueprint treats one structural fact honestly: borrowed normativity fails exactly where customers declared nothing. Many real workloads run without limits. Policy:

- **No resolvable bar ⇒ no crossable limit ⇒ ineligible for forecasting (Tier B)** and for threshold-kind checks; the entity is *not* silently skipped.
- The coverage report carries a **resolvability metric** — the fraction of selected targets with a config-sourced bar — and an explicit list of unbounded workloads marked "unbounded: no early-warning eligibility."
- Falling back to ontology defaults is permitted but visible: a default-sourced bar is a flagged, lower-trust bar, and default changes are high-blast-radius governance events (12).

### 3.5 Binding states and the coverage report

Every (entity, variable) pair lands in exactly one state: **bound** (collecting, validated or suspect), **unresolved** (expected for this entity type but not found/normalized), or **out-of-scope** (capability- or scope-excluded). The onboarding **coverage report** is a first-class deliverable: binding states per variable family, validation statuses, the resolvability metric, the unbounded-workload list, quarantined-stream counts from 03, and per-phenomenon observability (which phenomena are fully, partially, or not observable on this cluster — feeding degraded-match honesty in 07).

### 3.6 Re-binding

Binding re-runs on triggers, never continuously: new or lost exporters and signals; platform or version changes; topology-driven entity creation (new entities are instantiated automatically from types); operator override changes. Re-binding outputs feed re-selection (06).

### 3.7 Conceptual structures

| Structure | Fields (meaning) |
|---|---|
| Binding record | CEI; canonical variable; state (bound/unresolved/out-of-scope); validation status (verified/suspect/failed); equivalence variant matched; emission metadata; distro gate applied |
| Resolved bar | CEI; variable; kind; source (config/override/default, flagged); config path read; factor; resolved value; resolved-at; window |
| Coverage report | Per-family binding states; validation summary; resolvability metric; unbounded list; quarantine summary; per-phenomenon observability |

## 4. Epistemic discipline enforcement

Binding facts are MEASURED-class statements about the system's own visibility. The engine never guesses: unjoinable stays quarantined (03), unverifiable stays suspect, undeclared stays unbounded — each a visible state, not a silent assumption. Default-sourced bars are flagged so no surfaced check borrows more authority than its source carries.

## 5. Structural dependencies

**Consumes:** ontology types, groups, gates, and rules (02); CEIs, lifecycle, and normalized streams (03). **Provides:** the bound customer graph and resolved bars to observation (05), selection (06), detection (07), and forecasting (09); the coverage report to surfacing (10); QA targets to the harness (11); coverage gaps as curation input to governance (12).

## 6. Failure modes and honesty mechanisms

False equivalence — the silent corruptor; countered by §3.2 validation and harness regression. Over-instantiation noise (variables fanned onto entity types where they are meaningless) — countered by expected-entity-scope discipline in 02 and lints. Resolution drift (config changed but bar stale) — countered by re-binding triggers and resolved-at stamps. Hidden gaps — impossible by construction: every pair has a state, and the report enumerates them.

## 7. R&D execution sequence

1. **M1 — Binding resolver** (Phase 0b). Mechanisms §3.1 over reference clusters. Exit: every expected signal lands in a state with a stated reason.
2. **M2 — Per-instance instantiation, both identity layers** (Phase 0b). Exit: instance counts and per-instance bars verified against cluster config on reference clusters.
3. **M3 — Semantic validation suite** (Phase 0b, with 11). Exit: seeded false-equivalence fixtures are caught; validation statuses populate.
4. **M4 — Threshold resolution + resolvability accounting** (Phase 0b). Exit: resolvability metric and unbounded list published in the coverage report.
5. **M5 — Coverage report v1** (Phase 0b exit gate). Exit: report covers states, validation, resolvability, quarantine, and per-phenomenon observability.
6. **M6 — Re-binding reactivity** (Phase 1). Exit: exporter and topology change scenarios re-bind and notify selection within the agreed staleness budget.

## 8. Open questions owned here

How aggressively suspect bindings should participate in detection before verification (use-but-mark versus hold-out); whether per-phenomenon observability should gate phenomenon activation entirely or only annotate degraded matches (with 07); the minimum viable variant catalogue per equivalence group before onboarding a new exporter family is declared supported.
