# 07 — Topological Detection Engine

**Role in suite:** The "now" half of the runtime. Deterministic phenomenon matching across the graph's topology — entity-local, first-order, and second-order — plus cascade recognition and blast-radius derivation. The phenomena in the ontology *are* the detection units; there is no separate playbook artifact and therefore no second knowledge base to keep in sync.

---

## 1. Purpose and ownership

This document owns: match semantics (temporal ordering over fingerprint states); topological span traversal with edge-validity intersection; partial/degraded match policy under incomplete coverage; cascade recognition over authored relation edges; blast-radius derivation; the noise-suppression rule that a single crossing is not an alert; the determinism and replayability guarantee; and the sensitivity parameters that the harness calibrates.

It does not own: the phenomena themselves (02), fingerprints (05), the watch set (06), loud-but-unmatched routing (08), or anything predictive (09).

## 2. System context

Real failures rarely sit on one entity, so co-occurrence is not confined to a single entity: each phenomenon declares how far it spans and along which edge types its members are distributed, and detection walks exactly that far. Detection's output is the strongest claim the system makes about the present — a MEASURED co-occurrence in the authored pattern — and it is kept strictly descriptive: a topological match is a co-occurrence across the graph, not a proof of cause.

## 3. Core mechanics

### 3.1 Match semantics

A phenomenon **lights up** when its member signals reach their threshold states in the temporal pattern the graph encodes: `T0-` precursors elevated, `T0` present, optionally `T0+` following, with `required` members mandatory and `supporting` members strengthening. Inputs are fingerprints (05), grouped by the graph's phenomenon structure — the Part-5 arithmetic, organized by authored knowledge, nothing more.

### 3.2 Topological spans

| Span | Members live on | Example | Walk |
|---|---|---|---|
| **Zeroth-order** (entity-local) | One entity | A pod's memory near limit *and* rising | None |
| **First-order** | The entity plus direct neighbours along declared edge types | Node memory pressure: the node's own memory signal *and* eviction events on pods that run on it | One hop |
| **Second-order** | Two hops of propagation | Noisy neighbour: pod A's CPU saturation → the node's CPU pressure → sibling pod B throttling | Two hops; the traversal *is* the detection path |

```
   pod A ──runs-on──► node X ◄──runs-on── pod B
   (CPU saturating)   (CPU pressure)      (throttling)
      └──────── one phenomenon, 2-hop span ────────┘
   detection walks A → node X → B along runs-on edges
```

**Traversal contract (from 03):** walks follow only the phenomenon's declared edge types, and every traversed edge's validity interval must overlap the co-occurrence evaluation window. A condition "across" an edge that was not valid during the window is not a co-occurrence; a suspect (staleness-budget-exceeded) edge degrades the match rather than supporting it. This one rule prevents the engine from fabricating a 2-hop correlation through a stale edge — the single worst trust failure available to the product.

### 3.3 Degraded matches under partial coverage

Coverage (type + binding, 05 §3.3) applies along the walk: a neighbour found by traversal may contribute no signal. Policy: a phenomenon whose `required` members are observable and matching, but whose span is incompletely covered or whose supporting members are unbound, surfaces as a **degraded match**, explicitly marked with what was unobservable — reported honestly rather than dropped (hiding real findings) or inflated (claiming completeness it lacks). Per-phenomenon observability computed at binding time (04) pre-declares which phenomena can only ever match degraded on this cluster.

### 3.4 Cascades and blast radius

`Phenomenon relation` edges (trigger → downstream) usually manifest *across hops*: the trigger on one entity, the downstream on a neighbour. Detection uses them two ways:

- **Cascade recognition.** When a trigger phenomenon and its graph-declared downstream both light up on topologically related entities within the relation's window, the system surfaces them as a single correlated story — still descriptive: the authored relationship is currently manifest, nothing stronger.
- **Blast radius.** Given a phenomenon on entity A, walking the downstream relation edges across valid topology yields the entities the graph says are *at risk* — used to scope a current insight here, and to scope a forecast's early warning in 09. The radius is an authored relationship made concrete on this cluster's topology, never a model's prediction about the neighbours.

### 3.5 A single crossing is not an alert

A lone threshold crossing is just a fingerprint state. The **phenomenon match is the unit that surfaces** — which is what suppresses noise, since many signals wobble past bars routinely but few do so in the multi-signal, temporally ordered, topologically distributed pattern a phenomenon describes. The conjunction across signal multiplicity, temporal order, and topology is the engine's false-positive defence, multiplicative by construction.

### 3.6 Sensitivity parameters

How strict a match must be is tunable, never hard-coded: members required beyond the `required` set; ordering strictness (how firmly `T0-` must precede `T0`); span-completeness thresholds for degraded surfacing; cascade window widths. All defaults come from harness calibration (11) against the replay corpus, with operator-visible overrides via 10.

### 3.7 Determinism and replay

Same readings + same graph version + same topology snapshot (edges with validity) ⇒ same matches, always. Nothing on this path learns, forecasts, or randomizes. Every match emits with full derivation: the fingerprint states, entities, edges (with validity stamps), graph version, and parameter set that produced it — sufficient for byte-identical replay in the harness.

### 3.8 Conceptual structure: detection finding

| Field | Meaning |
|---|---|
| Phenomenon; graph version | Which authored pattern matched, under which knowledge release |
| Span instantiation | The concrete entities (CEIs) filling each entity role, and the edge path with validity stamps |
| Match quality | Full / degraded, with the unobservable members and suspect edges enumerated |
| Temporal evidence | Member states with timestamps establishing the ordering |
| Cascade linkage | Trigger/downstream finding references where a cascade is recognized |
| Blast radius | At-risk CEIs from the downstream walk, marked authored-relationship-derived |
| Provenance | MEASURED, with attached AUTHORED references (member notes, relation notes) — adjacent, never merged |

## 4. Epistemic discipline enforcement

Findings are MEASURED-class with AUTHORED references attached, never fused: the match says "these states co-occurred in this authored pattern across these entities," and the only "why" text shown is the graph's authored notes, attributed. Cascades are reported as authored edges currently manifest. Degraded marks and suspect-edge disclosure type the engine's own uncertainty. Detection runs whether or not forecasting exists, degraded, or is disabled — the non-gating rule is absolute.

## 5. Structural dependencies

**Consumes:** phenomena and relation edges (02); validity-aware topology (03); fingerprints and coverage (05); the Tier-A set with neighbourhoods (06). **Provides:** insights and cascades to surfacing (10); blast-radius derivation to forecasting (09); the matched/unmatched partition to the unexplained channel (08); replayable findings to the harness (11).

## 6. Failure modes and honesty mechanisms

False story through stale topology — eliminated by validity intersection (§3.2). Systematic miss or false fire from a wrongly authored temporal tag or span — caught offline by the falsification suite (11), never patched by runtime learning. Silent loss of findings under partial coverage — converted into visible degraded matches. Alert fatigue — countered structurally by §3.5 and calibrated sensitivity. Drift between deployed graph versions — every finding pins its graph version; governance (12) regression-tests phenomenon behaviour across releases.

## 7. R&D execution sequence

1. **M1 — Entity-local matcher** (Phase 0b). Zeroth-order matching over fingerprints with temporal ordering. Exit: golden phenomena fire correctly on replayed fixtures.
2. **M2 — First-order spans** (Phase 1). One-hop traversal with the validity contract. Exit: node-pressure-class phenomena match across real neighbourhoods; stale-edge fixtures degrade, never fabricate.
3. **M3 — Second-order spans** (Phase 1). Exit: noisy-neighbour-class fixtures match along the A → node → B path with full derivation.
4. **M4 — Degraded-match policy** (Phase 1). Exit: partial-coverage fixtures surface marked, with unobservables enumerated.
5. **M5 — Cascades and blast radius** (Phase 1). Exit: trigger/downstream fixtures surface as one story; radius walks honour validity.
6. **M6 — Sensitivity calibration** (Phase 1 exit gate, with 11). Exit: defaults justified by precision/recall on the replay corpus.

## 8. Open questions owned here

Which edge types are worth traversing for which phenomena, and whether two hops is the right ceiling (deeper walks risk spurious correlation — any extension beyond two hops requires harness evidence per cascade class); how cascade windows should scale with edge type; whether degraded matches should carry a quantitative completeness score (shared question with 01).
