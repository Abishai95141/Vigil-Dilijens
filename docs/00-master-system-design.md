# 00 — Master System Design
## Kubernetes AI Observability: Curated Ontology Graph + Narrow Forecasting Layer

**Status:** Blueprint v1.0 · Code-free architecture and development reference
**Document suite:** This master document plus thirteen component design documents (01–13). The master is self-sufficient: it contains everything needed to understand the whole system. The component documents add depth per subsystem and are themselves independently readable. Filenames are numbered in the order each subsystem enters active development; the true phased timeline, including parallel tracks, is held in `13-rd-execution-roadmap.md`.

---

## 1. The system in one paragraph

This is an AI-driven observability product for Kubernetes clusters, with single-node and edge clusters as first-class targets. One curated, versioned **ontology graph** holds everything the system knows at the type level: which signals a cluster can emit, how each signal generalizes across customer naming dialects and across every matching entity, how entities are wired together, and which multi-signal *phenomena* (failure and behaviour patterns) matter, in what temporal order, and across what topological span. At onboarding, a **binding engine** compiles this type-level knowledge against a specific customer's cluster, producing a **bound customer graph**: a live, instance-level model of that cluster with auto-calibrated, config-sourced thresholds. At runtime, a **selection engine** decides which entities are worth watching and how hard; a deterministic **detection engine** checks live measurements against the graph's phenomena, across each entity and its first- and second-order neighbourhood, to report what is happening **now**; a narrow **forecasting layer** estimates when gauge-like precursor signals will cross their configured limits **soon**. Both findings reach an operator through evidence-backed surfaces, alongside an honest channel for loud activity the graph cannot explain. The system never auto-remediates and never invents a causal explanation: the only "why" it shows is one the graph was authored to contain.

## 2. Problem forces and non-negotiable commitments

Four forces shape every decision in this blueprint:

1. **Schema heterogeneity.** Every customer names and exports metrics differently. Hard-coded metric names break on contact with a real cluster, so a variable must be defined once and generalize.
2. **Scale and cost.** A cluster holds far more entities than can be watched equally, and model-based forecasting of every eligible signal on every entity is infeasible. Monitoring must be selected and tiered.
3. **The AIOps trust problem.** Earlier "AI for ops" products lost operator trust by asserting "X caused Y" from correlation. This system structurally refuses to.
4. **Blind spots.** Knowledge-driven detection sees only what the graph describes, so loud activity matching nothing must still be flagged — and the residual limits of even that flagging must be stated.

The commitments that answer these forces are non-negotiable across all components:

- **Author once, generalize.** A variable and its threshold rule are written once and instantiate across customer dialects, across every matching entity, and across both identity layers (instance and role), auto-calibrated per entity.
- **Deterministic observation.** The path from live data to "which phenomenon is happening" is pure lookup-and-compare. No model inference and no learning sit on that path.
- **Narrow statistics.** The only arithmetic ever performed on a time series is threshold comparison, rate-of-change, and co-occurrence. No learned per-customer baselines exist anywhere in the system.
- **Borrowed normativity.** Thresholds come from the customer's own configuration — their declared limits are treated as their statement of what matters — never from learned behaviour. Where the customer declared nothing, the system says so rather than inventing a bar.
- **Reason over topology, not in isolation.** Symptoms are correlated across an entity and its graph neighbours, along authored edge types.
- **No invented causation.** The system surfaces only a *measurement*, a *forecast* (labelled extrapolation with uncertainty), or an *authored graph relationship*. It never derives causation from correlation. This is enforced as a type discipline, not a style preference (see §4 and `01`).
- **Honest partial coverage.** Binding gaps, degraded topological matches, unresolvable thresholds, and the unexplained channel's own residual blind spot are all reported, never hidden.
- **Forecasting is early warning, not certainty.** Uncertainty bands never collapse to a line, and forecast language is always "projected to cross," never "will cross."

## 3. The two pillars and the hidden third

The architecture rests on two named pillars and one structural bridge that deserves equal billing:

**Pillar 1 — the curated ontology graph (type level).** A single versioned artifact holding signals (with modality, data type, equivalence groups, capability prerequisites, distro gates), entity types and topology edge types, phenomena (multi-signal patterns with temporal-order tags, topological spans, and trigger→downstream relation edges), and threshold-sourcing rules. The reference graph at blueprint time holds 842 nodes, 3,759 edges, and 589 signals, of which 457 are Metric-modality numeric time series. The graph is the system's entire brain; everything else is mechanism. Specified in `02`; changed only through governance (`12`).

**Pillar 2 — the narrow forecasting layer.** A pretrained, zero-shot, univariate time-series model used strictly as a **clock**: given one bare, scale-normalized series and a resolved bar, it answers "when does this number cross that number," emitting a point projection plus a quantile band. It accepts no labels and no graph knowledge; it learns nothing per customer. The reference implementation is TimesFM 2.5 behind a model-agnostic clock interface, making the model swappable without touching any knowledge. Specified in `09`.

**The hidden third — the binding and generalization layer.** The ontology is type-level; a cluster is instance-level. The binding engine is the compiler between them: the ontology is source code, binding is compilation, and the **bound customer graph** is the running program. The clock is a runtime service the program calls with fully-resolved arguments. Most implementation risk lives in this bridge and in the identity layer beneath it, not in either named pillar. Specified in `03` (identity) and `04` (binding).

## 4. The epistemic discipline (summary of `01`)

Every datum the system produces or surfaces carries one of three **provenance classes** from birth:

| Class | What it is | Example statement | Source of truth |
|---|---|---|---|
| **MEASURED** | A fact read from the time-series store | "Memory is at 505 MiB, above its 486 MiB bar." | Quantitative State Store |
| **PROJECTED** | A labelled extrapolation with mandatory uncertainty | "Projected to cross 486 MiB in ~13 min (band 9–22 min)." | Clock output joined with a resolved bar |
| **AUTHORED** | A curated graph relationship, surfaced verbatim and attributed | "The graph records that rising working-set precedes OOM, and that an OOM here stresses node X." | Ontology graph, with author and version provenance |

The composition rule: classes may be **joined** — presented adjacently, each labelled — but never **fused** into a derived statement of a stronger class. A topological match is a co-occurrence, not a proof of cause. A cascade is an authored trigger→downstream edge lighting up, not an inferred cause. A forecast is never restated as fact. The join happens at exactly one place, the surfacing layer, and even there the three objects remain typed. Coverage statements are typed too: bound / unresolved / out-of-scope, full / degraded match, resolvable / unresolvable bar. The full charter, including prohibited derivations, language register rules, and per-component enforcement points, is `01-epistemic-separation-charter.md`, and every component's exit gates include charter conformance.

## 5. Architecture overview

### 5.1 Full dataflow

```
                        Customer's Kubernetes Cluster
                  ┌──────────────────────────────────────────┐
                  │  K8s API · Prometheus/OTel · Logs · Events │
                  └───────────────────┬──────────────────────┘
                                      │ raw telemetry + topology facts
        DISCOVERY-TIME (cold path)    │
   ┌──────────────────────────────────▼──────────────────────────────────┐
   │  IDENTITY & CORRELATION LAYER (03)   — prerequisite zero            │
   │  canonical entity identity · instance↔role mapping ·                │
   │  label normalization per exporter · timestamped topology edges      │
   └──────────────────────────────────┬──────────────────────────────────┘
                                      │ identified entities + edges
   ┌──────────────────────────────────▼──────────────────────────────────┐
   │  BINDING & GENERALIZATION ENGINE (04)                               │
   │  capability gating · equivalence-group match + semantic QA ·        │
   │  distro gates · per-instance variable instantiation ·               │
   │  threshold resolution (config → override → default) · coverage rpt  │
   └───────────────┬─────────────────────────────────┬───────────────────┘
                   │                                 │ canonical-named streams
                   ▼                                 ▼
   ┌───────────────────────────┐        ┌──────────────────────────────┐
   │  ONTOLOGY GRAPH (02)      │ identity│  QUANTITATIVE STATE STORE    │
   │  type-level, versioned,   │  join   │  (within 05) raw measurements│
   │  governed via (12)        │◄───────►│  one stream per entity·signal│
   │  + BOUND CUSTOMER GRAPH   │  keys   │  stores, never interprets    │
   └─────────────┬─────────────┘        └──────────────┬───────────────┘
                 │                                      │ series windows
        RUN-TIME │ (hot path, deterministic)            ▼
   ┌─────────────▼─────────────┐        ┌──────────────────────────────┐
   │  MONITORING SELECTION (06)│        │  OBSERVATION & FINGERPRINTS  │
   │  coverage gate · phenomenon│        │  (05) three primitives →     │
   │  participation · forecast │        │  per-entity fingerprints     │
   │  eligibility · criticality│        └──────────────┬───────────────┘
   │  · operator scope · tiers │                       │ fingerprints
   └──────┬──────────────┬─────┘                       │
          │ Tier A set   │ Tier B set                  │
          │ + neighbourhoods                           │
   ┌──────▼───────────┐  │   ┌──────────────────┐  ┌──▼───────────────────┐
   │ TOPOLOGICAL      │  │   │ FORECASTING (09)  │  │ UNEXPLAINED (08)     │
   │ DETECTION (07)   │  └──►│ clock/reasons/join│  │ loud + unmatched →   │
   │ phenomenon match │      │ decompose·forecast│  │ flag for human       │
   │ 0/1st/2nd order  │      │ ·project vs bar · │  │ investigation        │
   │ cascades · blast │      │ early-warning     │  │ (residual blind spot │
   │ radius   (NOW)   │      │ candidates (SOON) │  │  stated honestly)    │
   └──────┬───────────┘      └─────────┬─────────┘  └──────────┬──────────┘
          │ MEASURED+AUTHORED          │ PROJECTED(+AUTHORED refs)│ MEASURED
          └───────────────┬────────────┴───────────────────────┬─┘
                          ▼          THE JOIN (only here)      ▼
   ┌─────────────────────────────────────────────────────────────────────┐
   │  SURFACING & OPERATOR EXPERIENCE (10)                               │
   │  insights (now) · early warnings (soon) · unexplained · topology    │
   │  view · timeline · chat · context windows · configuration surface   │
   └─────────────────────────────────────────────────────────────────────┘

   META PATH (offline, continuous):
   VALIDATION & CALIBRATION HARNESS (11)  ←→  GRAPH GOVERNANCE (12)
   binding QA · phenomenon falsification · sensitivity calibration ·
   forecast backtests · per-phase exit gates · graph release regression
```

### 5.2 The two-pillar interaction contract

The coupling between graph and clock is **asymmetric, unidirectional, and concentrated at exactly five points.** The graph feeds the forecaster everything except the numbers; the forecaster feeds the graph nothing.

1. **Target selection.** The graph decides what gets forecast: the entity selection of `06` intersected with the signal eligibility funnel of `09`. The model never chooses its own work.
2. **The bar.** The config-relative threshold rule resolves the limit per instance. "Time-to-cross" is not a model output; it is a join of model trajectory with graph normativity.
3. **Meaning.** A `T0-` precursor tag is what converts "a number crosses a number at ~10:13" into "early warning for a named phenomenon."
4. **Scope.** Blast radius comes from authored trigger→downstream relation walks, never from the model.
5. **Decomposition.** The one place graph knowledge reaches *into* the model's input pipeline: graph-explained event footprints are subtracted from the series before inference, with the reason staying outside the model as the human-facing explanation.

Equally binding is the **negative space** — what never flows: the model never sees labels, graph structure, or reasons (bare floats plus scale normalization only); the graph never ingests model output (no learned edges, no feedback loop); detection never waits on forecasting (parallel, non-gating). The forecasting layer therefore contributes exactly one thing — kinematics. All semantics, normativity, consequence, and spatial scope are graph-supplied. Any conformant clock can replace the reference model without touching a single phenomenon.

### 5.3 Dependency DAG

```
01 charter ─────────────────────────────► (normative input to everything)
02 ontology ─┬─► 04 binding ─► 05 observation ─► 06 selection ─► 07 detection ─► 08 unexplained
03 identity ─┘        │              │                 │              │               │
                      │              └────────────► 09 forecasting ◄──┘               │
                      │                                   │                           │
                      └──────────► 10 surfacing ◄─────────┴───────────────────────────┘
11 harness  (parallel from Phase 0b; gates every phase exit)
12 governance (active from first graph release; sole write path to 02)
13 roadmap   (sequencing authority)
```

The graph is the single point of semantic failure; the identity layer is the single point of referential failure; the clock is fully replaceable. Robustness work concentrates accordingly.

## 6. Component inventory

| # | Document | Core logic held | Depends on | Depended on by |
|---|---|---|---|---|
| 01 | Epistemic Separation Charter | Provenance classes, composition rules, prohibited derivations, language register, enforcement points | — | All |
| 02 | Ontology Graph Specification | Type-level knowledge schema: signals, equivalence groups, entity/edge types, phenomena, threshold rules; authoring invariants | 01 | 03–12 |
| 03 | Identity & Correlation Layer | Canonical entity identity, instance↔role model, label normalization, identity lifecycle, timestamped topology edges and freshness semantics, graph↔TSDB join contract | 01, 02 | 04–09 |
| 04 | Binding & Generalization Engine | Discovery-time compilation: capability gating, equivalence resolution + semantic QA, distro gates, three-axis instantiation, threshold resolution and resolvability accounting, coverage report | 02, 03 | 05–09, 10 |
| 05 | Observation & Fingerprint Pipeline | TSDB division of labour, stream model, the three primitives, coverage axes, per-entity fingerprints, determinism | 03, 04 | 06–09 |
| 06 | Monitoring Selection Engine | Selection funnel, neighbourhood expansion, intensity tiers A/B, criticality weighting, operator scope, re-selection | 02, 04, 05 | 07, 09 |
| 07 | Topological Detection Engine | Phenomenon matching across 0/1/2-hop spans, temporal ordering, degraded matches, cascade recognition, blast radius, determinism and replay | 02, 03, 05, 06 | 08, 09, 10 |
| 08 | Unexplained Anomaly Channel | Precise loudness definition, unmatched-activity routing, residual blind spot statement, bounded summarization constraints, curation feedback | 05, 07 | 10, 12 |
| 09 | Forecasting Layer | Clock interface and swap contract, target eligibility funnel, per-target pipeline, decomposition, covariate admission, guardrails, horizon envelope, calibration gating | 02, 04, 05, 06, 07 | 10 |
| 10 | Surfacing & Operator Experience | Three surfaces, topology view with separated current/predictive marks, timeline, chat constraints, context windows, configuration surface, evidence rules | 01, 07, 08, 09 | Operators |
| 11 | Validation & Calibration Harness | Replay substrate, binding QA, phenomenon falsification, sensitivity calibration, forecast backtests, phase exit gates | 01–09 | 12, 13 |
| 12 | Graph Governance & Release Engineering | Versioning, change classes and blast radius, review workflow, bound-graph migration, staged rollout, curation intake | 02, 08, 11 | 02 (sole write path) |
| 13 | R&D Execution Roadmap | Phase definitions 0a–5, entry/exit gates, parallel tracks, open-question ledger | All | Program management |

## 7. End-to-end pipelines

**Pipeline A — Discovery and binding (cold path, re-runs on change).** Cluster telemetry and topology facts → identity layer mints canonical entity identities and timestamped edges → binding engine gates capabilities, resolves equivalence groups with semantic QA, applies distro gates, instantiates every applicable variable across every matching entity with a per-instance resolved bar → bound customer graph + honest coverage report (bound / unresolved / out-of-scope; resolvable / unresolvable bars).

**Pipeline B — Observation to detection (hot path, deterministic).** Canonical-named measurements stream into the Quantitative State Store → the three primitives evaluate per selected entity → fingerprints materialize → detection groups fingerprint states by the graph's phenomena, walking declared topological spans with edge-validity intersection → full or degraded matches, cascades, and blast radii emit as MEASURED findings carrying AUTHORED references. Same readings + same graph version + same topology snapshot → same matches, always.

**Pipeline C — Forecasting (warm path, parallel, never gating).** Tier-B targets → fetch context window → subtract graph-explained footprints (abort if over-explained) → clock inference → optional known-future covariate → recombine scheduled footprints → project against the resolved bar → time-to-cross point and band → emit an early-warning candidate only if it crosses within the horizon with adequate confidence; otherwise silence.

**Pipeline D — Surfacing and the operator loop.** Findings render on three visually distinct, evidence-backed surfaces; operators drill to per-entity, per-signal evidence; operator actions (scope pins, threshold overrides, context windows marking planned events) flow back as configuration — context windows doubling as the timestamped splice points decomposition needs.

**Pipeline E — Validation and governance (meta path).** Recorded readings + graph versions + topology snapshots replay deterministically through the harness; phenomenon falsification, sensitivity sweeps, and forecast backtests gate each phase; the unexplained channel and coverage gaps feed candidate authoring into governance; humans author, the harness regresses, releases roll out staged with rollback.

## 8. Robustness architecture: the risk register

| Risk | Why it is structural | Owning doc | Mitigation built into this blueprint |
|---|---|---|---|
| Graph↔TSDB identity join is wrong | Every downstream behaviour is only as correct as this join; failures are silent | 03 | Identity layer promoted to prerequisite zero; exact-match join on canonical identity; quarantine of unjoinable series; join-accuracy exit gate for Phase 0a |
| False semantic equivalence in bindings | One bad equivalence silently corrupts every downstream check for that customer | 04, 11 | Binding QA suite: unit, scope, cumulative-vs-instantaneous, platform-variant checks; bindings carry validation status |
| Stale topology edges during detection walks | A stale `runs-on` edge fabricates a false 2-hop match — the worst failure for a trust-first product | 03, 07 | Edges are timestamped assertions with validity intervals; walks intersect edge validity with the co-occurrence window |
| Unresolvable thresholds (workloads without limits) | Borrowed normativity fails exactly where customers declared nothing; risky workloads go invisible to early warning | 04 | Resolvability metric in the coverage report; explicit unbounded-workload policy: listed, never silently skipped |
| Unfalsifiable phenomena | Temporal-order tags and spans are empirical claims; wrong ones cause systematic misses or false stories | 11 | Phenomenon falsification suite against labeled incidents; precision/recall per phenomenon; sensitivity calibrated from data |
| Mis-calibrated forecast bands | A few badly-timed warnings destroy trust faster than no forecasting | 09, 11 | Backtest gate per target class before any operator-visible warning; band-coverage verification; horizon envelope stated |
| The unexplained channel's own blind spot | Without baselines, "loud" requires a bar or rate guard; novel failures through un-thresholded signals stay invisible | 08 | Loudness defined precisely; residual blind spot stated in non-goals and in the coverage report rather than implied away |
| Ontology edits with global blast radius | One bad default propagates to every customer using the fallback | 12 | Change classes with scaled review rigor; harness regression on every release; staged rollout and rollback |

## 9. Boundaries (non-goals)

No auto-remediation — the system surfaces and explains; the operator acts. No learned per-customer baselines — thresholds are config-sourced; forecasting is zero-shot. No model-invented causation — only MEASURED, PROJECTED, and AUTHORED statements. No forecasting of non-series modalities (events, logs, state, traces, profiles, audit), raw counters-as-levels, or histograms-as-wholes. No separate playbook artifact and no lab-provenance machinery in the product — the graph is the single knowledge source, and falsification lives offline in the harness. No monitoring of everything equally — selection and tiering are deliberate. No pretence of coverage — binding gaps, degraded matches, unresolvable bars, and the unexplained channel's residual blind spot (novel failures expressing only through signals that carry no bar or rate guard) are all stated. Forecasting is early warning, not certainty — bands never collapse to a line.

## 10. R&D phasing summary

Full definitions, entry/exit gates, and parallel tracks are in `13`. In brief:

| Phase | Scope | Headline exit gate |
|---|---|---|
| 0a — Foundations | Charter ratified; ontology schema v1; identity & correlation layer; store conventions | Identity-join accuracy on reference clusters |
| 0b — Binding & local observation | Binding engine with QA; instantiation; coverage report; primitives + fingerprints; selection (Tier A); entity-local detection; harness v1 | Deterministic replay; resolvability and coverage metrics published |
| 1 — Topological detection | 1- and 2-hop matching; degraded matches; cascades; blast radius; unexplained channel | Phenomenon precision/recall on the replay corpus |
| 2 — Marquee forecasting | Clock interface + reference adapter; eligibility funnel; projection; first precursor warnings (working-set → OOM) | Backtest calibration gate per shipped target class |
| 3 — Decomposition | Context-window splice points; per-event-class footprint models; abort criteria | Footprint-removal improves backtest error without harming band coverage |
| 4 — Known-future covariates | Covariate admission for schedules/calendars | Covariate targets beat univariate baseline in backtests |
| 5 — In-context refinement (optional) | Inference-time example conditioning | Hard-gated on a confirmed loadable checkpoint for the deployed model line |

## 11. Glossary

**Ontology graph** — the single curated, versioned, type-level knowledge artifact. **Bound customer graph** — the generated, instance-level projection of the ontology onto one cluster. **Variable / signal** — a thing the cluster can emit, authored once, generalized across names, entities, and identity layers; only Metric-modality signals are numeric series. **Equivalence group** — the customer-name variants mapping to one canonical variable. **Phenomenon** — a named multi-signal pattern with temporally tagged members, a topological span, and relation edges; the unit of detection and the sole source of "reasons." **Fingerprint** — the materialized three-primitive state for one entity. **Canonical entity identity (CEI)** — the normalized key joining graph nodes to time-series streams. **Tier A / Tier B** — broad cheap detection / narrow expensive forecasting. **Clock / reasons / join** — model timing / graph meaning / surfacing composition, never merged. **MEASURED / PROJECTED / AUTHORED** — the three provenance classes. **Blast radius** — entities the graph's authored downstream edges say are at risk. **Context window** — an operator-declared planned event with scope and time, used for suppression and as a decomposition splice point.

---

*One discipline binds the whole blueprint: a measured state, an authored relationship, and a forecast are three different epistemic objects. They may meet on a screen; they never merge in the data.*
