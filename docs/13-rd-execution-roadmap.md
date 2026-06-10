# 13 — R&D Execution Roadmap

**Role in suite:** The sequencing authority. Documents 01–12 each carry their own milestone ladder; this document is where those ladders interlock into one chronological program — phases, parallel tracks, entry criteria, measurable exit gates, and the open-question ledger that says which unknowns block which phase.

---

## 1. Purpose and ownership

This document owns: the global dependency DAG; the phase definitions (0a, 0b, 1, 2, 3, 4, 5) with scope, entry criteria, exit gates, and explicit not-yet lists; the parallel-track map; the ordering rationale; and the consolidated open-question ledger.

It does not own any component's internal design. Where this document and a component document disagree on sequence, this document wins; where they disagree on mechanics, the component document wins.

**Numbering convention:** file numbers 01–12 encode the order each subsystem *enters active development*; this roadmap holds the true timeline, in which several tracks run in parallel and the harness (11) and governance (12) start far earlier than their numbers suggest.

## 2. System context

The system is one curated ontology graph plus a narrow forecasting clock, compiled per customer by a binding engine, evaluated by a deterministic detection engine, and surfaced under a strict three-class provenance discipline (MEASURED / PROJECTED / AUTHORED, joined only at the screen, never fused). The build order below is risk-driven, not feature-driven: the silent failure modes (identity mis-joins, false equivalences, unfalsified phenomena, mis-calibrated bands) are attacked before the visible features they would corrupt.

## 3. Global dependency DAG

```
01 charter (ratify first; normative input to everything)
   │
   ├── 02 ontology schema ──┐
   │                        ├── 04 binding ── 05 observation ── 06 selection ── 07 detection ─┬─ 08 unexplained
   └── 03 identity ─────────┘                                                                 │
                                                                                              ├─ 09 forecasting
        10 surfacing  ◄── consumes 04 (coverage report) from Phase 0b,                        │
                          then 07/08 (Phase 1), then 09 (Phase 2) ◄───────────────────────────┘
        11 harness    ◄── parallel track from Phase 0b; certifies every phase exit
        12 governance ◄── parallel track from the first graph release (Phase 0b)
```

Three structural facts drive the ordering. The **identity layer (03) is prerequisite zero**: every downstream behaviour is only as correct as the graph↔store join, and join failures are silent — so it is built and gated before anything consumes it. The **harness (11) starts in Phase 0b**, not at the end: authored knowledge is empirical and must be falsifiable from the moment it starts mattering. The **clock (09) comes after the detection foundation**: forecasting borrows all of its meaning from graph structures (precursor tags, bars, blast radius) that must already be compiled, evaluated, and trusted.

## 4. Phase definitions

### Phase 0a — Foundations
**Scope:** Charter ratified (01 M1–M2). Ontology schema v1 and reference-graph encoding begun (02 M1–M2). Identity layer complete: CEI scheme, normalization maps for the core exporter families, lifecycle state machine, timestamped-edge semantics (03 M1–M4). Store conventions (05 M1).
**Entry:** the structural review's recommendations accepted; reference clusters available.
**Exit gates:** join accuracy ≥ target on reference clusters with all misses explained as quarantines (03 M5 / 11 gate row 0a); CEI scheme survives churn-scenario review; charter signed off.
**Explicitly not built yet:** no binding, no detection, no surfaces, no model anywhere.

### Phase 0b — Binding and local observation
**Scope:** Binding engine with semantic QA, two-layer instantiation, threshold resolution, resolvability accounting, coverage report (04 M1–M5). Primitives, fingerprints, replay interface (05 M2–M5). Selection gates 1–2 with reason codes, Tier-A only (06 M1, M3-partial). Entity-local detection (07 M1). Coverage-report surface — the first thing a customer-facing screen shows is the honest visibility map (10 M1). Harness v1: replay substrate, binding QA, charter battery started (11 M1–M2, M6-start). First immutable graph release with pinning (12 M1).
**Entry:** Phase 0a gates passed.
**Exit gates:** deterministic replay byte-identical; binding QA fixtures pass; resolvability and coverage metrics published; entity-local phenomena fire correctly on replay fixtures.
**Not yet:** no topological walks, no cascades, no unexplained channel, no forecasting, no early-warning surfaces.

### Phase 1 — Topological detection
**Scope:** First- and second-order spans with the edge-validity traversal contract, degraded matches, cascades, blast radius, sensitivity calibration (07 M2–M6). Neighbourhood closure, criticality weighting, operator scope, re-selection (06 M2, M4, M6). Unexplained channel: loudness, routing, surface card, curation reports (08 M1–M4). Surfacing: insight feed, topology view with current marks, unexplained surface, timeline (10 M2–M4). Harness: falsification corpus v1, phenomenon suite, sensitivity sweeps (11 M3–M4). Governance: change classes, review workflow, regression gates, migration (12 M2–M4). Re-binding reactivity (04 M6). Evidence linkage in the graph (02 M4).
**Entry:** Phase 0b gates passed.
**Exit gates:** phenomenon precision/recall on the corpus meets per-class targets; stale-edge fixtures degrade rather than fabricate (the trust-critical test); sensitivity defaults evidence-backed; a behavioural-class graph change has landed through the full governance workflow.
**Not yet:** still no model inference anywhere; the word "projected" does not yet appear in the product.

### Phase 2 — Marquee forecasting
**Scope:** Clock interface and reference adapter; eligibility funnel and projection with guardrails; backtest gate; first warning class live — container working-set → OOM, blast radius attached (09 M1–M4). Tier-B budgeting (06 M5). Backtest suite operative (11 M5); charter battery complete (11 M6). Early-warning cards and predictive marks, context windows begun, chat begun (10 M5–M7-start). Staged rollout and rollback exercised (12 M5); curation loop closed end-to-end (12 M6 with 08 M4). Optional bounded summarizer, if shipped at all (08 M5).
**Entry:** Phase 1 gates passed; Tier-B selection structure in place.
**Exit gates:** **the backtest calibration gate passes for every shipped warning class before any operator sees a warning**; register audit clean on all early-warning strings; a seeded bad release is caught at canary and rolled back in exercise.
**Not yet:** no decomposition (raw context windows feed the clock; targets with heavy known-event pollution are simply skipped), no covariates, no in-context refinement.

### Phase 3 — Decomposition
**Scope:** Context-window splice points, per-event-class footprint models, abort criterion, recombination of scheduled footprints (09 M5). Context-window and configuration surfaces complete; chat complete; mobile/on-call (10 M6–M8).
**Entry:** Phase 2 gates passed; context-window adoption sufficient to supply splice points.
**Exit gate:** decomposition demonstrably improves backtest error without degrading band coverage (11 gate row 3).
**Not yet:** no covariates.

### Phase 4 — Known-future covariates
**Scope:** Covariate admission (scheduled jobs, calendars) through the auxiliary pathway; per-class lift evaluation (09 M6).
**Entry:** Phase 3 gate passed.
**Exit gate:** covariate targets beat their univariate baselines in backtests (11 gate row 4).

### Phase 5 — In-context refinement (optional)
**Scope:** Inference-time example conditioning of the clock (09 M7).
**Entry — a hard gate, not a date:** a loadable in-context checkpoint confirmed for the *deployed* model line. At blueprint time the in-context variant exists on the base model line only; if the gate never clears, the architecture loses nothing — the clock contract stands and Phases 0–4 are complete without it.
**Exit gate:** its own backtest gate per 11, same standard as any clock change.

## 5. Parallel-track map

| Track | Runs | Notes |
|---|---|---|
| Knowledge (02, 12) | 0a → continuous | Authoring and governance never stop; releases are routine by Phase 1 |
| Runtime-deterministic (03, 04, 05, 06, 07, 08) | 0a → 1, then maintenance | The product's trust core; complete before any model ships |
| Forecasting (09) | 2 → 5 | Strictly after the deterministic core is trusted and gated |
| Surfacing (10) | 0b → 3 | Begins with the coverage report, ends with mobile parity |
| Validation (11) | 0b → continuous | Certifies every exit gate above; never a phase of its own that "finishes" |

## 6. Open-question ledger

| # | Question | Owner doc | Blocks | Resolution path |
|---|---|---|---|---|
| 1 | Per-edge-type staleness budgets; tombstone retention horizon | 03 | Phase 0a exit quality | Empirical, from reference-cluster churn data |
| 2 | Suspect-binding participation policy (use-but-mark vs hold-out) | 04 | Phase 0b detection quality | Decide at 04 M3 with seeded-fixture evidence |
| 3 | Degraded-match representation (qualitative mark vs completeness score) | 01/07 | Phase 1 surfacing | Operator-facing trial during 10 M2 |
| 4 | Two-hop ceiling on traversal; cascade window scaling per edge type | 07 | Phase 1 scope | Harness evidence required before any deeper walk |
| 5 | Sensitivity-tuning exposure to operators vs held policy | 06/10 | Phase 1–2 config surface | After 11 M4 sweeps establish safe ranges |
| 6 | Recurrence thresholds and cross-customer aggregation for curation intake | 08/12 | Phase 1–2 intake loop | Data-isolation review first; per-customer default |
| 7 | Forecast surfacing thresholds (max useful band width, min horizon) | 09 | Phase 2 exit | Answered by 11 M5 backtests, not intuition |
| 8 | Horizon/cadence defaults per target class | 09/11 | Phase 2–3 | Published per class from backtest evidence |
| 9 | Which graph events beyond operator context windows are clean splice points | 09 | **Most gating for Phase 3** | Inventory + timestamp-quality audit during Phase 2 |
| 10 | Histogram-quantile targets in scope at all | 02/09 | Phase 3–4 scope | Cost/benefit after gauge classes are calibrated |
| 11 | In-context checkpoint availability on the deployed model line | 09 | Phase 5 entry (hard gate) | Verify loadable artifact; do not start otherwise |

## 7. Ordering rationale (why this sequence and no other)

Identity before binding, because a compiler over wrong references compiles confident nonsense. Binding before observation, because a fingerprint against an unvalidated bar is noise with a timestamp. Deterministic detection before any model, because the product's trust claim — nothing on the path from data to "what is happening" learns or guesses — must be demonstrably true and gated before the one component that *does* extrapolate is allowed near an operator. The harness from 0b, because every later gate in this roadmap is only as real as the machinery that certifies it. Governance from the first release, because the day the graph starts mattering is the day its edits start having blast radius. And forecasting last among the runtime layers, precisely because it is the most replaceable part of the system: the clock contributes kinematics only, and a program sequenced this way could ship Phases 0a–1 as a complete, honest, deterministic observability product before a single model inference ever runs.

---

*End of suite. Master: `00-master-system-design.md`. Constitution: `01-epistemic-separation-charter.md`. Everything else is mechanism, in the order risk demands.*
