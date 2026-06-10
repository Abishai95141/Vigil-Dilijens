# 10 — Surfacing & Operator Experience

**Role in suite:** The join — the only place in the architecture where the three provenance classes meet, each still wearing its label — and the operator's complete interface: three finding surfaces, the topology view, the timeline, chat, context windows, and configuration. The system surfaces and explains; the operator decides and acts.

---

## 1. Purpose and ownership

This document owns: the three finding surfaces and their separation; the topology view with strictly distinct current and predictive marks; the insight feed and anomaly timeline; the chat surface and its answer constraints; context windows and their dual purpose; the configuration surface; evidence and drill-down rules; and on-call/mobile delivery.

It does not own: any finding production (07/08/09), any knowledge (02), or any threshold semantics (04) — it renders, composes adjacently, and routes operator intent back as configuration.

## 2. System context

Trust is won or lost here. Upstream, the architecture guarantees that measurements, projections, and authored relationships never merge in the data; this layer guarantees they never merge on screen or in language. Every card is evidence-backed and drillable; every claim carries its provenance class; predictive content is visually and verbally unmistakable as prediction.

## 3. Core mechanics

### 3.1 The three finding surfaces

| Surface | Content | Provenance composition | Canonical phrasing |
|---|---|---|---|
| **Insights (now)** | A phenomenon currently matched — possibly across a 1- or 2-hop span — full or degraded | MEASURED match + AUTHORED member/relation notes, adjacent | "Matched: node memory pressure across node X and 4 pods (degraded: 1 member unbound)." |
| **Early warnings (soon)** | A projection of a precursor across its bar, with band, the graph's "what this precedes," and the blast radius | PROJECTED + AUTHORED references, adjacent | "Projected to cross its configured limit in ~13 min (band 9–22). Known OOM precursor per the graph. At risk per the graph: node X and siblings." |
| **Unexplained (anomalous)** | Loud-but-unmatched activity | MEASURED only, conspicuously without a reason | "Anomalous — investigate. Not yet explained by any curated pattern." |

Visual identity is per-surface and consistent everywhere (feed, topology, timeline, mobile): an operator can classify a finding's epistemic strength at a glance before reading a word.

### 3.2 Topology view

The bound customer graph rendered live: entities as nodes, valid edges as links (suspect edges visibly distinct, per 03). Overlaid, in **separated visual languages**: *current-condition marks* (matched phenomena, loud entities) and *predictive marks* (early-warning targets) — never the same glyph family, so "is" and "might" cannot be confused at a glance. Cascade stories draw the trigger→downstream path; blast radii highlight the at-risk set with their authored-relationship sourcing one tap away. Per-node and per-edge drill-down opens the underlying evidence: fingerprint states, bar provenance, edge validity stamps, member notes.

### 3.3 Feed, timeline

The **insight feed** orders current findings by recency and operator scope, degraded marks always visible. The **anomaly timeline** lays findings of all three surfaces on time — matches as intervals, projections as forward-pointing ranges (band-shaped, never a point), unexplained cards as aging spans — making "what happened, what is happening, what is projected" one read without class confusion.

### 3.4 Chat — "ask the cluster"

Answers compose exclusively from the three classes, each in its register (01 §5): measurements quoted with values and provenance; forecasts always modal with bands; authored notes attributed to the graph. The mandatory honesty clause: when something is not established, the answer says so plainly — "no curated pattern explains this; it is flagged unexplained" — and never improvises a bridge. Chat may aggregate and navigate; it may not infer, diagnose, or upgrade a class.

### 3.5 Context windows — operator knowledge in, two uses out

Operators mark planned events (deploys, batch jobs, maintenance) with **scope + time window**. Two consumers: **suppression** — expected disturbances inside a declared window are damped on the surfaces rather than raised as findings; and **decomposition splice points** — the declared timestamps are exactly what the forecasting layer's footprint subtraction needs (09 §3.4). One operator gesture, two systems served; this is the primary channel through which human knowledge of the future enters the product.

### 3.6 Configuration surface

Threshold overrides (becoming the operator-override tier of resolution, 04 §3.4, with the previous source still visible); monitoring scope pins and exclusions (overriding selection, 06 §3.1, reason-coded); context window management; sensitivity settings exposed per the 07/11 calibration outcomes; and the standing **coverage report** (04) as a first-class page — what is bound, suspect, unresolved, out-of-scope, unbounded, and which phenomena can only match degraded here.

### 3.7 Mobile / on-call

Early warnings and high-criticality insights deliverable to on-call with the same class marks and the same phrasing rules; a warning on a phone still says "projected," still shows its band, still links to evidence.

## 4. Epistemic discipline enforcement

This layer is the charter's last line: register rules applied at render; class labels mandatory on every card; current/predictive visual separation absolute; provenance drill-down universal; degraded and suspect marks never elided for visual cleanliness. The join composes adjacently — a warning card *contains* a projection and *cites* authored edges; it never paraphrases them into a fused causal sentence. Periodic register audits (01 M4) run against every operator-visible string.

## 5. Structural dependencies

**Consumes:** findings from 07, 08, 09; the bound customer graph and coverage report (04); validity-aware topology (03); charter rules (01). **Provides back:** operator overrides to 04, scope to 06, sensitivity to 07, context windows to 09 — the full operator-intent return path. **Audited by** 11's charter tests.

## 6. Failure modes and honesty mechanisms

Class confusion on screen — prevented by separated visual languages and register audits. Alert fatigue — mitigated upstream (07 §3.5, 09 silences) and here by scope-aware feeds, aging, and suppression windows. Over-suppression via careless context windows — windows are scoped and time-bounded, visible on the timeline, and findings inside them are damped-with-disclosure rather than deleted. Evidence dead-ends — structurally impossible: a card without a complete derivation path is a release-blocking defect.

## 7. R&D execution sequence

1. **M1 — Coverage report surface** (Phase 0b). The first thing a customer sees is the honest map of what the system can and cannot watch. Exit: report page live from onboarding.
2. **M2 — Insight feed, entity-local** (Phase 0b–1). Exit: full/degraded marks and drill-down to fingerprint evidence.
3. **M3 — Topology view, current marks** (Phase 1). Exit: spans and cascades render with edge-validity visibility.
4. **M4 — Unexplained surface + timeline** (Phase 1). Exit: aging, supersede-on-match, and the not-yet-explained mark verified.
5. **M5 — Early-warning cards + predictive marks** (Phase 2, after 09's calibration gate). Exit: register audit clean; band always rendered; blast radius cited to the graph.
6. **M6 — Context windows + configuration surface** (Phase 2–3). Exit: suppression and splice-point consumption both verified end to end.
7. **M7 — Chat** (Phase 2–3). Exit: adversarial prompt fixtures cannot elicit a class upgrade, a causal improvisation, or an unqualified future tense.
8. **M8 — Mobile/on-call** (Phase 3). Exit: parity of marks and registers on the constrained form factor.

## 8. Open questions owned here

How much sensitivity tuning (07) to expose versus hold as policy; whether degraded matches need a dedicated feed filter; chat paraphrase policy for authored notes (shared with 01); the minimum useful band rendering on mobile widths.
