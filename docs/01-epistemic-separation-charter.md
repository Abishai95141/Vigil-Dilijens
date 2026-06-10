# 01 — Epistemic Separation Charter

**Role in suite:** The constitution of the system. Every other document inherits these rules; every component's exit gates include conformance to this charter. Ratified before any component enters development.

---

## 1. Purpose and ownership

This document owns the system's trust guarantee: the rules that make it structurally impossible — not merely discouraged — for the product to convert correlation into causation, extrapolation into fact, or curated knowledge into model output. It defines the three provenance classes, the composition rules between them, the prohibited derivations, the language register per class, and the enforcement point inside every component.

It does not own any runtime mechanism. Enforcement mechanics live in the components; this charter defines what they enforce.

## 2. System context

The system detects current failure phenomena and forecasts imminent threshold crossings in Kubernetes clusters, driven by one curated ontology graph and one narrow forecasting model. Its predecessors in the AIOps market lost operator trust by asserting "X caused Y" from statistical correlation. This charter is the structural answer: the system's claims are partitioned into classes with different epistemic strength, and the partitions are typed into the data itself, from birth to screen.

## 3. The three provenance classes

Every statement-bearing datum the system creates carries exactly one class, assigned at creation and immutable thereafter.

| Class | Definition | Created by | Mandatory attributes |
|---|---|---|---|
| **MEASURED** | A fact read from the Quantitative State Store, or a deterministic arithmetic consequence of such facts (a threshold state, a rate, a co-occurrence, a phenomenon match) | Observation (05), Detection (07), Unexplained (08) | Timestamp; entity identity; the readings and resolved bar it derives from |
| **PROJECTED** | An extrapolation of a series against a resolved bar, with uncertainty | Forecasting (09) | The `is_projection` mark; point estimate; an uncertainty band that never collapses to a line; horizon; qualitative confidence derived from band width |
| **AUTHORED** | A relationship a human curator wrote into the ontology graph, surfaced verbatim | Ontology authoring via Governance (12) | Graph version; author provenance; the authored note text, unaltered |

Coverage and honesty statements are typed as a corollary of MEASURED: binding states (bound / unresolved / out-of-scope), match completeness (full / degraded), and bar resolvability (resolvable / unresolvable) are facts about the system's own visibility and are surfaced with the same rigor as facts about the cluster.

## 4. Composition rules

**Join, never fuse.** Classes may be *joined* — presented adjacently, each carrying its own label — at exactly one place in the architecture: the surfacing layer (10). An early warning is the canonical join: a PROJECTED crossing, next to the AUTHORED precursor edge that gives it meaning, next to the AUTHORED blast-radius walk that gives it scope. The three remain three. No component may *fuse* classes: no derived statement may claim a stronger class than its weakest input, and no statement may be re-emitted with its class upgraded.

**Concretely prohibited derivations:**

- Co-occurrence (MEASURED) → causal claim. A topological phenomenon match is a co-occurrence across the graph, never a proof of cause.
- Cascade observation (MEASURED + AUTHORED) → inferred causation. A cascade is the graph's authored trigger→downstream edge lighting up; the system reports that the authored relationship is currently manifest, nothing stronger.
- PROJECTED → MEASURED. A forecast is never restated as a fact, not in feeds, not in chat answers, not in summaries.
- Model output → graph content. Nothing the clock produces is ever written into the ontology or the bound customer graph. There is no learned edge, weight, or threshold anywhere.
- Generated text → AUTHORED. No language model may generate a "reason." The only reasons in the system are notes a human authored into the graph. Where bounded generation is permitted at all (the unexplained channel's optional summarizer), its output is a MEASURED-class enumeration of which signals are loud on which entities, with causal vocabulary prohibited.

**Permitted graph→model flows (exhaustive list).** The graph may influence the model in exactly two ways, both outside the model's inference channels: (a) selecting which series the clock runs on, and (b) subtracting a graph-explained event footprint from the input series before inference, with the reason staying outside the model. The model's input is otherwise a bare float sequence. It receives no labels, names, units, or graph structure.

**Non-gating rule.** Detection never waits on forecasting. The deterministic path must produce identical results whether the forecasting layer is running, degraded, or absent.

## 5. Language register

Surfaced language is the last enforcement surface and the first thing an operator judges. Each class has a register:

- MEASURED: indicative present or past. "Is at." "Crossed at 10:13." "Matched with two of three members observable (degraded)."
- PROJECTED: explicitly modal and bounded. "Projected to cross ~10:13, band 10:09–10:22." Never "will cross," never "is about to fail," never an unqualified future tense.
- AUTHORED: attributed. "The graph records that…" / "Per the curated ontology…" The note is shown as written, with its provenance available on drill-down.

Chat answers compose from these registers only and must say plainly when something is not established ("no phenomenon in the graph explains this; it is flagged as unexplained").

## 6. Enforcement points

| Component | What it must check |
|---|---|
| 02 Ontology | Every "reason" string carries author + version provenance; default thresholds are flagged as defaults |
| 04 Binding | Coverage states emitted honestly; no silent guesses on unjoinable or unverifiable bindings |
| 05 Observation | Outputs carry MEASURED class with derivation references; no statistics beyond the three primitives |
| 07 Detection | Matches emit MEASURED with AUTHORED references attached, never merged; degraded matches marked |
| 08 Unexplained | No causal vocabulary; not-yet-explained mark mandatory; summarizer (if any) bounded as above |
| 09 Forecasting | `is_projection` mandatory; band mandatory; only references to authored edges, never generated causal text; silence on flat/wide/non-crossing forecasts |
| 10 Surfacing | Register rules at render; current vs predictive marks visually distinct; provenance drill-down everywhere |
| 11 Harness | Charter-conformance tests are part of every suite; a class-fusion anywhere is a release-blocking defect |
| 12 Governance | Sole write path to AUTHORED content; review required for every reason note |

## 7. Structural dependencies

**Consumes:** nothing. **Provides:** the normative rule set inherited by documents 02–12 and tested by 11. This charter has no upstream; it is the root of the dependency DAG.

## 8. R&D execution sequence

1. **M1 — Ratify the charter** (Phase 0a entry). Agree the three classes, composition rules, and prohibited-derivation list with all component owners. Exit: signed-off v1 of this document.
2. **M2 — Class carriage convention.** Define how provenance class, derivation references, and graph version travel with every datum across component boundaries (a data-contract concern, not a code concern). Exit: every component design (02–10) names where the class field lives in its conceptual structures.
3. **M3 — Conformance checklist.** Produce the per-component checklist of §6 in testable form for the harness (11). Exit: harness v1 includes charter tests.
4. **M4 — Register audit.** Before each surfacing milestone in 10, audit all operator-visible strings against §5. Exit: zero unqualified future tense or causal verbs outside AUTHORED quotations.

## 9. Open questions owned here

Whether degraded-match honesty should carry a quantitative completeness figure or a qualitative mark (interacts with detection sensitivity, 07/11); whether chat may ever paraphrase an AUTHORED note for brevity or must always quote it (current rule: quote, paraphrase only with the original one tap away).
