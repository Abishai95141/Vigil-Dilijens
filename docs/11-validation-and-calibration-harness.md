# 11 — Validation & Calibration Harness

**Role in suite:** The falsification engine. The graph makes empirical claims (equivalences, temporal orders, spans); the forecaster makes statistical claims (bands, crossings). This harness is the offline machinery that tests both *before operators are exposed to them*, and its suite results are the exit gates of every phase. It begins in Phase 0b — not at the end of the program — because authored knowledge that cannot be falsified is the architecture's deepest hidden risk.

---

## 1. Purpose and ownership

This document owns: the deterministic replay substrate; the four validation suites (binding QA, phenomenon falsification, detection sensitivity calibration, forecast backtesting); the charter-conformance tests; the per-phase exit-gate definitions consumed by the roadmap (13); and the continuous regression that runs on every graph release (12).

It does not own: the components it tests (02–10), the corpus-collection operations of specific reference clusters, or release decision authority (12 decides; the harness informs and blocks on defined gates). It is **offline tooling, not product**: nothing in this document ships into a customer cluster, and no lab-provenance machinery leaks into runtime surfaces.

## 2. System context

Two facts make this harness load-bearing. First, the deterministic path (05/07) guarantees that same readings + same graph version + same topology snapshot ⇒ same fingerprints and matches — which means the entire detection half of the product is *replayable by construction*, and therefore testable to byte-identical standards. Second, the system's two knowledge forms are both fallible in ways runtime cannot self-correct: the graph never learns (by commitment), and the clock never sees ground truth (by design). The only correction loop that exists anywhere is this harness feeding humans (via 12) and gating releases. A trust-first product whose phenomena were never fired against labeled failures, or whose bands were never checked against held-out reality, is trust theatre; this harness is the difference.

## 3. Core mechanics

### 3.1 The replay substrate

The foundation under all four suites: a recorded bundle of **(readings, graph version, topology snapshot with edge validity intervals, resolved-bar set, parameter set)** that re-runs through the real observation and detection components (05/07) and the real forecast pipeline (09) to produce findings deterministically. Properties: byte-identical re-runs for the deterministic path; pinned-version everything (a replay names its graph release, normalization-map versions, and parameter set); time-shiftable (a bundle replays "as of" any instant inside its window, which is what backtesting needs). Bundles come from reference clusters, chaos runs, reproduced incidents, and — with consent and isolation — anonymized customer captures.

### 3.2 Suite A — Binding QA

Tests the compiler's semantic claims (04 §3.2) plus the join beneath it (03):

- **Equivalence validity:** seeded fixtures of true and false equivalences (unit mismatches, cumulative-vs-instantaneous confusions, scope mismatches, platform-variant traps such as cgroup v1/v2 accounting differences) must classify correctly into verified / suspect / failed.
- **Join audits:** sampled streams from replay bundles checked against control-plane truth; mis-joins are release-blocking, quarantines are counted and reasoned.
- **Resolvability accounting:** the coverage report's resolvability metric and unbounded-workload list verified against the bundle's actual config.
- **Normalization-map regression:** every exporter-family map re-validated when the family's version moves.

### 3.3 Suite B — Phenomenon falsification

The graph's temporal tags and spans are empirical claims; this suite makes them earn their place:

- **Corpus:** labeled incidents — chaos-engineering runs (induced OOMs, node pressure, noisy neighbours), reproduced failure scenarios, and post-incident captures — each labeled with what actually happened and when.
- **Per-phenomenon questions:** does it fire on the failure class it describes (recall)? does it stay quiet on corpus segments where that failure is absent (precision)? do the `T0-`/`T0`/`T0+` orderings match observed reality (tag verification)? does the declared span match where the symptoms actually appeared (span verification)?
- **Outputs:** precision/recall per phenomenon, published; tag and span discrepancies filed as curation items to 12 — the harness never edits the graph itself.
- A phenomenon with no corpus coverage is marked **untested**, and untested status is visible in governance review (12), so unfalsified knowledge is at least never mistaken for validated knowledge.

### 3.4 Suite C — Detection sensitivity calibration

The tunables of 07 §3.6 (members-required beyond the required set, ordering strictness, span-completeness thresholds for degraded surfacing, cascade window widths) are swept against the falsification corpus to find defaults with evidence: each shipped default carries the precision/recall trade-off curve that justified it. Re-runs whenever the corpus grows or the graph's phenomena change materially.

### 3.5 Suite D — Forecast backtesting

The gate between the clock and the operator (09 M3). Per **target class** (e.g. container working-set, node filesystem usage, PVC utilization):

- **Band coverage:** over held-out history, do X% quantile bands contain the realized values X% of the time? Mis-calibration in either direction fails the class.
- **Time-to-cross error:** for realized crossings, the distribution of (projected vs actual) crossing-time error, point and band.
- **Silence correctness:** the guardrail behaviours (flat series, wide bands, non-crossing projections) verified to stay silent.
- **Horizon/cadence suitability:** the empirical answer to which horizons are trustworthy per class — published as the class's horizon envelope (09 §3.7).
- **Decomposition lift (Phase 3):** footprint subtraction must improve error without degrading band coverage, per 09 M5; **covariate lift (Phase 4)** likewise per 09 M6.

**The gate rule:** no warning class becomes operator-visible until its backtest gate passes, and any clock swap or upgrade re-runs every shipped class's gate before rollout.

### 3.6 Charter-conformance tests

Mechanical checks from 01 §6 run across all suites' outputs: every finding carries its provenance class and derivation references; no PROJECTED datum appears restated as MEASURED anywhere downstream; no generated text carries AUTHORED class; forbidden inputs never reach the clock (adversarial fixtures attempt label leakage); surfacing strings pass the register audit. A class-fusion anywhere is a release-blocking defect.

### 3.7 Exit gates (consumed by 13)

| Phase | Gate the harness certifies |
|---|---|
| 0a | Join accuracy ≥ target on reference bundles; all misses are quarantines, not mis-joins |
| 0b | Deterministic replay byte-identical; binding QA fixtures pass; resolvability and coverage metrics published |
| 1 | Phenomenon precision/recall on the corpus meets per-class targets; stale-edge fixtures degrade rather than fabricate; sensitivity defaults evidence-backed |
| 2 | Backtest gate passed for every shipped warning class; charter tests clean on early-warning surfaces |
| 3 | Decomposition lift demonstrated without band-coverage loss |
| 4 | Covariate lift demonstrated per admitted covariate class |
| 5 | (Entry, not exit) loadable in-context checkpoint confirmed for the deployed model line, then its own backtest gate |

### 3.8 Conceptual structures

| Structure | Fields (meaning) |
|---|---|
| Replay bundle | Readings; topology snapshot with validity; graph version; bar set; normalization-map versions; parameter set; labels (incident annotations); window |
| Suite result | Suite; bundle set; per-item outcomes; metrics (precision/recall, band coverage, error distributions); pass/fail against gate; produced-at; versions pinned |
| Curation item | Source suite; discrepancy description; affected graph element; evidence references — routed to 12 |

## 4. Epistemic discipline enforcement

The harness is the discipline's test bench (§3.6), and it observes the discipline itself: suite results are facts about system behaviour under pinned versions, never edits to knowledge; discrepancies become curation items for humans, preserving the humans-author-only rule. Untested-status visibility extends the honesty commitment to the knowledge base's own validation state.

## 5. Structural dependencies

**Consumes:** the determinism guarantees of 05/07 (which make replay possible); components 03–09 as systems-under-test; the charter (01) as test oracle; labeled corpora from chaos and incident operations. **Provides:** exit-gate certification to the roadmap (13); release-blocking regression and curation items to governance (12); calibrated defaults to detection (07) and surfacing thresholds to forecasting (09).

## 6. Failure modes and honesty mechanisms

Corpus bias (phenomena validated only against the failures someone bothered to induce) — countered by untested-status visibility and a corpus-coverage report per graph release. Overfitting sensitivity defaults to the corpus — countered by held-out bundle splits and re-sweeps on corpus growth. Gate erosion under schedule pressure — countered structurally: gates are defined here, owned by 13, and a release that skips one is by definition out of process (12). Replay drift (components evolving past recorded bundles) — countered by pinned versions and bundle re-recording policy.

## 7. R&D execution sequence

1. **M1 — Replay substrate** (Phase 0b). Bundles, pinning, byte-identical re-run on the deterministic path. Exit: 05 M5 satisfied; first regression runs.
2. **M2 — Binding QA suite** (Phase 0b, with 04 M3). Exit: seeded fixtures and join audits gate Phase 0a/0b claims.
3. **M3 — Falsification corpus v1 + phenomenon suite** (Phase 1). Chaos-run corpus covering the marquee failure classes; precision/recall pipeline. Exit: Phase 1 gate operative.
4. **M4 — Sensitivity sweeps** (Phase 1). Exit: 07 M6's evidence-backed defaults delivered.
5. **M5 — Forecast backtest suite** (Phase 2). Exit: 09 M3's gate operative for the first warning class.
6. **M6 — Charter-conformance battery** (Phase 0b start, complete by Phase 2). Exit: class-fusion and register checks wired into release regression.
7. **M7 — Continuous regression** (from first graph release, with 12). Every release re-runs binding QA, falsification, and shipped-class backtests. Exit: regression is a mandatory release step.

## 8. Open questions owned here

Per-phenomenon precision/recall targets (uniform versus criticality-weighted); minimum corpus coverage before a phenomenon may ship at all versus ship-as-untested; anonymization standards for customer-derived bundles; how often sensitivity sweeps re-run as the corpus grows without thrashing shipped defaults.
