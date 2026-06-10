# 12 — Graph Governance & Release Engineering

**Role in suite:** The change machinery for the system's brain. The ontology graph is not static truth; it is a deployed knowledge artifact whose edits have customer-facing blast radius. This document owns the sole write path to the graph: how knowledge is proposed, reviewed, validated, versioned, released, migrated, rolled back, and grown — with humans authoring every assertion, always.

---

## 1. Purpose and ownership

This document owns: graph versioning and release packaging; the change-class taxonomy and its scaled review rigor; the review workflow wired to harness regression; bound-customer-graph migration on upgrade; staged rollout and rollback; the curation intake loop; and authorship/audit provenance.

It does not own: the schema being governed (02), the validation machinery it invokes (11), the binding that consumes releases (04), or runtime behaviour of any kind. Governance is a human-and-process layer with tooling, not a runtime component.

## 2. System context

Everything the system can detect, every reason it can show, and every default bar it can fall back to lives in one versioned artifact. That concentration is the architecture's strength — one knowledge source, no playbook drift — and its sharpest operational risk: a single bad default threshold propagates to every customer relying on the fallback; a single mis-authored temporal tag systematically corrupts a phenomenon everywhere at once. The blueprint's answer is release engineering applied to knowledge: immutable versions, blast-radius-scaled review, mandatory regression, staged rollout, fast rollback. The same loop is also how the system grows — the unexplained channel (08), coverage gaps (04), and falsification discrepancies (11) all terminate here as proposals for *humans* to author.

## 3. Core mechanics

### 3.1 Versioning and packaging

The graph ships as **immutable, semantically versioned releases**. Every bound customer graph **pins** the release it was compiled from (04), every detection finding and forecast candidate stamps the graph version it ran under (07/09), and every harness result pins the version it tested (11) — so any behaviour anywhere is traceable to an exact knowledge state. Nothing edits a released graph; change means a new release.

### 3.2 Change classes and blast radius

Review rigor scales with how far a change reaches:

| Change class | Examples | Blast radius | Review rigor |
|---|---|---|---|
| **Additive, low** | New equivalence variant; new signal; new entity-type metadata | New capability only; existing behaviour untouched | Domain review + lints + binding QA on affected families |
| **Behavioural, medium** | New phenomenon; edit to members, tags, or span; relation-edge change; sensitivity-relevant metadata | Detection behaviour changes wherever the phenomenon binds | Domain review + falsification-suite run for the phenomenon (untested status blocks ship-as-validated) + replay diff on reference bundles |
| **Normative, high** | Default-threshold change; threshold-rule kind or factor change; equivalence-group semantic redefinition | Every customer on the fallback, or every binding through the group — the widest reach in the system | Senior domain review + full regression (binding QA, falsification, shipped-class backtests where bars feed forecasts) + staged rollout mandatory + explicit rollback plan |

The class is declared in the proposal and verified in review; misclassification is itself a release-blocking defect.

### 3.3 Review workflow

Proposal → authoring lints (02 M3) → domain review (a human curator who can defend the assertion against evidence) → harness regression scaled to class (11 M7) → release candidate → staged rollout. Every reason note, tag, span, and default in the proposal carries the author's identity and the evidence references the assertion rests on (02 §3.6); review without evidence is rejection by default. The harness can **block**; it cannot approve — approval is human.

### 3.4 Migration of bound customer graphs

A new release does not silently mutate live customers. Upgrade means **re-binding**: the customer's cluster is recompiled (04) against the new release, producing a binding diff — variables gained or lost, bars re-resolved, validation statuses changed, per-phenomenon observability shifted — and the diff drives re-selection (06 M6) and is visible in the coverage report (10 M1). High-class changes surface their effect in operator-readable terms before activation ("the default bar for X changes from A to B on N workloads currently using the fallback").

### 3.5 Staged rollout and rollback

Releases roll out in stages — reference clusters, canary customers, fleet — with the harness's continuous regression and live health signals (mis-join rates from 03, binding QA statuses from 04, finding-volume diffs from 07) checked at each stage. Rollback is re-pinning to the prior immutable release plus re-binding; because findings stamp their graph version, post-rollback behaviour is cleanly attributable. Default-threshold (normative-class) changes additionally support per-customer deferral, since an operator override (04 §3.4) is sometimes the better remedy than fleet-wide rollback.

### 3.6 Curation intake — how the graph grows

Three feeds terminate here, none of which writes to the graph itself:

- **Candidate phenomena** from the unexplained channel (08 §3.6): recurring loud-but-unmatched patterns, aggregated with evidence.
- **Coverage gaps** from binding (04): unresolved-but-expected signals, suspect equivalences, unbounded-workload clusters suggesting missing default policy.
- **Falsification discrepancies** from the harness (11 §3.3): tags or spans contradicted by the corpus.

Each becomes a triaged proposal in the workflow above. The loop closes the system's only learning path — **the product never learns; the knowledge base grows, through humans** — and the unexplained surface visibly shrinks as authored coverage expands, which is itself a trust signal.

### 3.7 Conceptual structures

| Structure | Fields (meaning) |
|---|---|
| Graph release | Version; immutable content hash; changelog by change class; lint/regression results; rollout stage state |
| Change proposal | Class (declared + verified); affected elements; author; evidence references; review state; harness results |
| Binding diff | Customer; from-version → to-version; gained/lost bindings; bar re-resolutions; observability shifts |
| Audit record | Element; author; version introduced; evidence references; review trail |

## 4. Epistemic discipline enforcement

Governance is where AUTHORED-class integrity is manufactured: sole write path, human authorship with identity and evidence on every assertion, generated text barred from entering (01 §4), defaults flagged at the source so surfacing can disclose them. Version pinning everywhere makes the discipline auditable in time — any surfaced statement can be traced to the exact authored knowledge state behind it.

## 5. Structural dependencies

**Consumes:** the schema and lints (02); regression and curation items (11); candidate reports (08); coverage gaps (04). **Provides:** released graph versions to binding (04) and thereby to everything downstream; migration diffs to selection (06) and surfacing (10); the audit provenance that 01 and 10 rely on. **Sole write path to** 02.

## 6. Failure modes and honesty mechanisms

Fleet-wide damage from a normative change — countered by class-scaled rigor, mandatory staging, deferral, and fast rollback. Knowledge stagnation (intake queue ignored) — countered by making intake volume and age visible metrics of the governance process itself. Review rubber-stamping — countered by evidence-required-by-default and harness blocking power. Version sprawl across the fleet — countered by supported-version policy and re-binding tooling that makes upgrades cheap. Silent semantic drift (same name, changed meaning) — countered by treating semantic redefinition as normative-class, never additive.

## 7. R&D execution sequence

1. **M1 — Versioning and packaging** (Phase 0b, with the first reference-graph release). Immutable releases; pinning wired through 04/07/09/11. Exit: every finding and suite result names its graph version.
2. **M2 — Change classes + review workflow** (Phase 0b–1). Proposal tooling, lints in path, domain review roles. Exit: first behavioural-class change lands through the full workflow.
3. **M3 — Harness-wired regression gates** (Phase 1, with 11 M7). Exit: regression is mechanically mandatory per class; a skipped gate cannot release.
4. **M4 — Migration and binding diffs** (Phase 1). Exit: reference-customer upgrade produces a correct, operator-readable diff and re-selection.
5. **M5 — Staged rollout + rollback** (Phase 2). Exit: a seeded bad release is caught at canary and rolled back cleanly in exercise.
6. **M6 — Curation intake loop** (Phase 1–2, with 08 M4). Exit: an unexplained-channel candidate travels intake → authored phenomenon → release → the original pattern now matches.

## 8. Open questions owned here

Supported-version window for customer pins (how long may a customer lag); whether canary selection should be opt-in or representative-by-design; cross-customer aggregation rules for intake evidence under data isolation (shared with 08); the cadence of routine releases versus event-driven ones.
