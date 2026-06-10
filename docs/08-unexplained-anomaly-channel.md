# 08 — Unexplained Anomaly Channel

**Role in suite:** The blind-spot patch, honestly bounded. Knowledge-driven detection recognizes only phenomena the graph describes; this channel guarantees that loud activity matching *nothing* is still surfaced for human investigation — and it states plainly what even this channel cannot see.

---

## 1. Purpose and ownership

This document owns: the precise definition of "loud" in a system with no learned baselines; the routing rule from loud-but-unmatched to the operator; aging and deduplication; the constraints on optional bounded summarization; the explicit statement of the channel's residual blind spot; and the feedback loop from recurring unexplained patterns into human curation.

It does not own: the primitives that make loudness evaluable (05), phenomenon matching (07), or graph authoring (02/12 — this channel proposes candidates; humans author).

## 2. System context

The fourth problem force: knowledge-driven detection only sees what the graph describes. The system's answer is not to bolt on a learned anomaly detector — that would reintroduce per-customer baselines, the thing the commitments forbid — but to route everything *loud* that detection could not *explain* into a clearly marked "anomalous — investigate" surface, with no causal claim attached.

## 3. Core mechanics

### 3.1 Loudness, defined precisely

Without learned baselines, loudness can only be expressed in the system's existing primitive vocabulary (05). An entity is **loud** within a window if and only if at least one of:

- a **bar crossing**: any thresholded signal on the entity is at `above` or `well-above` on its resolved bar; or
- a **rate excursion**: any rate-guarded signal exceeds its authored rate-of-change guard.

Nothing else qualifies. There is no distribution distance, no novelty score, no "looks unusual." This keeps the channel deterministic, explainable, and consistent with the no-baselines commitment.

### 3.2 The residual blind spot, stated

Because loudness requires a resolved bar or an authored rate guard, **signals carrying neither can never be loud — so a novel failure expressing itself only through un-thresholded, un-guarded signals is invisible even to this channel.** This is a deliberate cost of refusing learned baselines, and the blueprint treats it as a first-class boundary: it is recorded in the master non-goals, restated in the coverage report (which already lists unbounded workloads, 04 §3.4), and never implied away. The honest claim is: this channel covers *known signals exhibiting unknown patterns*, not *unknown signals*.

### 3.3 Routing rule

Per evaluation window: an entity (or neighbourhood) that is loud, and that participates in **no full or degraded phenomenon match** covering the loud states within the match window (07), routes to the unexplained surface as **"anomalous — investigate."** Marked: not-yet-explained; no causal vocabulary; full MEASURED evidence attached (which signals, which states, which bars with provenance).

### 3.4 Aging and deduplication

Persistent loudness collapses into one aging card rather than a stream of repeats; resolution of the loud states retires the card; a subsequent phenomenon match that *does* cover the states supersedes the card with a link (the unexplained item became explained — by authored knowledge, not by inference).

### 3.5 Optional bounded summarization

If a language-model summarizer is used at all, it operates under hard constraints from the charter (01): input is the enumerated loud states; output is a MEASURED-class *description* — which signals are loud on which entities, in what pattern of timing — with causal verbs prohibited, no reference to reasons, and the not-yet-explained mark mandatory. The summarizer compresses evidence; it never explains. Plain surfacing without any summarizer is the default and is fully sufficient.

### 3.6 Curation feedback loop

Recurring unexplained patterns are the graph's growth signal. The channel aggregates recurrences (same signal sets, similar topological arrangements, across time or customers) into **candidate-phenomenon reports** for human curators via governance (12). Humans author; the system never writes to the graph. This closes the knowledge loop without ever crossing the authored/inferred line.

### 3.7 Conceptual structure: unexplained finding

| Field | Meaning |
|---|---|
| Scope | The loud entity CEIs (and neighbourhood, if loudness clusters topologically) |
| Loud states | The bar crossings and rate excursions, each with bar/guard provenance and timestamps |
| Match check | The phenomena evaluated and why none covered these states (no participation / ordering unmet / span unmet) |
| Status | New / aging / superseded-by-match / resolved |
| Mark | "Anomalous — investigate"; not-yet-explained; MEASURED provenance |

## 4. Epistemic discipline enforcement

Everything surfaced here is MEASURED, and conspicuously *not* AUTHORED: the channel's defining property is the absence of a reason, kept visible. No causal vocabulary, no generated explanation, no silent suppression. The honesty extends reflexively: the channel discloses its own coverage limit (§3.2).

## 5. Structural dependencies

**Consumes:** fingerprints and bar/guard provenance (05); the matched/unmatched partition (07). **Provides:** the unexplained surface to 10; candidate-phenomenon reports to 12. **Independent of** forecasting (09) entirely.

## 6. Failure modes and honesty mechanisms

Noise from routinely wobbling signals — bounded by the same bar/guard vocabulary detection uses plus aging/dedup; if a signal is loud weekly and never matches, that is itself a visible curation signal (a missing phenomenon or a bad bar). Operator desensitization — countered by collapsing persistence into single aging cards and by the supersede-on-match link showing the channel converts into knowledge over time. The §3.2 blind spot — not fixable within the commitments; stated instead.

## 7. R&D execution sequence

1. **M1 — Loudness evaluator** (Phase 1). The two-clause definition over fingerprints. Exit: deterministic loud-set per window on replay fixtures.
2. **M2 — Routing and dedup** (Phase 1). Match-check against 07 output; aging; supersede-on-match. Exit: fixture suites for each status transition.
3. **M3 — Surface card** (Phase 1, with 10). Exit: card carries full evidence and the not-yet-explained mark; charter language audit passes.
4. **M4 — Curation feedback reports** (Phase 1–2, with 12). Exit: recurrence aggregation produces actionable candidate-phenomenon reports.
5. **M5 — Bounded summarizer (optional)** (Phase 2+). Exit: constraint tests prove no causal vocabulary and no reason references under adversarial fixtures; otherwise ship without it.

## 8. Open questions owned here

Recurrence thresholds for candidate reports (per-customer versus cross-customer aggregation, with the data-isolation implications of the latter); whether loudness should ever consider `at-threshold` states for high-criticality roles; the aging horizon before a never-matching loud pattern escalates from card to curation report automatically.
