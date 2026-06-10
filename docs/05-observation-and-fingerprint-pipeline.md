# 05 — Observation & Fingerprint Pipeline

**Role in suite:** The deterministic measurement substrate: the Quantitative State Store's division of labour, the three primitives that are the system's entire statistical vocabulary, the coverage axes, and the per-entity fingerprints that detection reads. Everything here is arithmetic; nothing here learns.

---

## 1. Purpose and ownership

This document owns: the store's role and stream model; the three primitives (threshold, rate-of-change, co-occurrence) as the complete and closed set of operations ever performed on a series; the two coverage axes and their non-conflation rule; fingerprint materialization; and the determinism guarantee on this path.

It does not own: identity and labels (03), bars and bindings (04), which entities are evaluated (06), how primitive states compose into phenomena (07), or any forecasting (09).

## 2. System context

The graph and the store are physically separate and logically joined by canonical entity identity: the graph answers *what is this, what is it wired to, where is its bar*; the store answers *what are its numbers doing*. Neither is useful alone. The system's trust posture depends on this path being boring: live data to "which phenomenon is happening" is pure lookup-and-compare, with no model inference and no learned baseline anywhere on it.

## 3. Core mechanics

### 3.1 The store's division of labour

The Quantitative State Store holds raw numbers over time — one stream per (canonical entity identity, canonical variable) — and it **stores and retrieves, never interprets**. Streams are stamped with CEI at ingest (03) and named canonically after binding (04). Retention is tiered by purpose: a hot window sized for fingerprint evaluation, a warm window sized for forecast context fetches (09). Cadence class travels with the stream (scrape, watch, event-driven, streaming); only regular-cadence streams are candidates for forecasting, but all bound streams serve detection.

### 3.2 The three primitives — all the arithmetic there is

| Primitive | Operation | Inputs | Output |
|---|---|---|---|
| **Threshold** | Is the latest value past its bar — subtraction | Stream; resolved bar (04) | Threshold state |
| **Rate-of-change** | Smoothed first difference over a window — differencing | Stream; window | Rate summary; rate-guard state where a rule exists |
| **Co-occurrence** | Several conditions true at once within a window — conjunction | Member states; window | Co-occurrence state |

This vocabulary is closed by commitment: no learned baseline, no seasonal model, no distribution fit, no anomaly score exists on this path. Co-occurrence generalizes from one entity to a topological neighbourhood in 07; the primitive itself does not change, only the set of entities contributing member states.

### 3.3 The two coverage axes — never conflated

- **Coverage** — which entities have usable data: set by *type scope* (the variable applies to this entity type; pods, nodes, and containers are richly instrumented, while services and policies are mostly structural) and *binding* (the customer exports it and it bound, per 04).
- **Detection scope** — which entities to correlate across: set by *topology* (the phenomenon's declared span, walked in 07).

Two facts hold them apart. **Metrics attach to entities, never to edges** — relationships carry no time series. And **coverage is set by type plus binding, not by graph distance** — a first-order neighbour can be metric-poor while a second-order one is metric-rich. Traversal finds the neighbours; coverage decides which of them actually contribute a signal. A topological phenomenon can therefore be only *partially* observable, and that partiality is reported honestly as a degraded match (07), never papered over.

### 3.4 Entity fingerprints

Per selected entity, the live primitive results are materialized into a compact **fingerprint**: a threshold-status ladder per thresholded variable (below / at-threshold / above / well-above), rate summaries per rate-guarded variable, and a co-occurrence window state — each component stamped with evaluation time and the bar source it used (config / override / default-flagged). Detection reads fingerprints, not raw history; this keeps the hot path cheap and the evidence trail exact (every fingerprint component can cite the readings and bar it derived from).

### 3.5 Determinism

Same readings + same resolved bars + same windows ⇒ same fingerprints, always. The path is replayable by construction, which the harness (11) relies on for every suite it runs.

### 3.6 Conceptual structures

| Structure | Fields (meaning) |
|---|---|
| Stream descriptor | CEI; canonical variable; cadence class; retention tier; binding/validation status reference |
| Fingerprint | CEI; per-variable threshold state with bar provenance; per-variable rate summary; co-occurrence window state; evaluated-at; derivation references |

## 4. Epistemic discipline enforcement

Every output here is MEASURED-class with derivation references attached (the readings, the bar, the window). Bar provenance rides along so a default-sourced state never silently carries config-sourced authority. The closed primitive vocabulary is itself an enforcement: there is no operation available whose output could masquerade as more than arithmetic.

## 5. Structural dependencies

**Consumes:** CEI-stamped streams (03); bindings and resolved bars (04). **Provides:** fingerprints to detection (07) and the unexplained channel (08); context windows of raw series to forecasting (09); coverage facts to selection (06) and the coverage report (04). **Replay substrate for** the harness (11).

## 6. Failure modes and honesty mechanisms

Stale fingerprints under ingest lag — evaluation stamps make staleness visible; consumers treat over-age fingerprints as missing, not as fresh. Bar drift between resolution and evaluation — bar provenance carries resolved-at; re-binding triggers refresh (04). Counter resets and scrape gaps — handled inside the rate primitive's smoothing rules and recorded, never extrapolated over. Partial observability — expressed as coverage facts and degraded matches, by design.

## 7. R&D execution sequence

1. **M1 — Stream conventions** (Phase 0a, with 03). CEI stamping, canonical naming, cadence classes, retention tiers. Exit: reference-cluster telemetry lands in well-formed streams.
2. **M2 — Primitive evaluators** (Phase 0b). The three operations with windowing, smoothing, and reset handling. Exit: golden-case fixtures (crossings, resets, gaps) evaluate correctly and deterministically.
3. **M3 — Fingerprint materializer** (Phase 0b). Exit: fingerprints carry full derivation references; entity-local detection (07 M1) runs on them.
4. **M4 — Coverage accounting** (Phase 0b). Exit: per-phenomenon observability computed and feeding the coverage report (04 M5).
5. **M5 — Replay interface** (Phase 0b, with 11). Recorded readings replay byte-identically. Exit: harness v1 runs its first deterministic regression on this path.

## 8. Open questions owned here

Hot-window sizing per cadence class (cost versus fingerprint richness); whether well-above ladder steps should be authored per variable family or held as a global convention; how watch/event-driven modalities are best represented in fingerprints (state presence and recency rather than numeric ladders) without expanding the primitive vocabulary.
