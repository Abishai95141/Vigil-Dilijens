# 01 — The Testing Framework (what it is)

> Source: `TESTING.md` at repo root (authored by the prior AI, "Antigravity"). This file
> summarizes its structure. The framework runs tests/gates that **already existed in the repo** —
> the prior AI wrote the *playbook*, not the tests themselves.

## Shape: 3 phases · 9 scenarios · 5 guarantees · 11 gates

### The 5 guarantees (G1–G5)
- **G1 Epistemic Integrity** — MEASURED / AUTHORED / PROJECTED never fused (provenance firewall + no strings on the model wire).
- **G2 Restraint Under Ignorance** — says "I don't know"; never invents causation or bars.
- **G3 Deterministic Replay** — same input ⇒ byte-identical findings (SHA-256 digests).
- **G4 Forecast Honesty** — bands never collapse, flat stays flat, quantiles never cross.
- **G5 Operational Lightness** — small CPU/RAM footprint, CGO-free single binary.

### The 9 scenarios
| ID | Name | Guarantee | Infra |
|---|---|---|---|
| S1 | Provenance Firewall | G1 | live cluster |
| S2 | gRPC Boundary (no strings on model wire) | G1 | none (`just test`) |
| S3 | Unknown Anomaly (the restraint test) | G2 | live cluster OR corpus |
| S4 | Ontology Fork | G2 | offline/governance |
| S5 | Replay Guarantee | G3 | cluster → offline replay |
| S6 | Honest Forecast | G4 | none (`just clockd-test`) |
| S7 | Overhead Proof | G5 | live cluster |
| S8 | Transitive Cascade / Interdependency (Theme 2) | — | corpus + live |
| S9 | AI Claim Referee / NLP (Theme 2) | — | corpus + live API |

### The 3 phases
- **Phase 1 — Contract tests (no cluster, ~10 min):** `just ci` + `just clockd-test` + `just harness-test` + `CGO_ENABLED=0 go build ./...`.
- **Phase 2 — Corpus gates (no cluster, ~5 min):** the 11 `just *-gate` recipes over frozen corpora in `corpus/`.
- **Phase 3 — Live cluster demo (~30 min):** boot `obsd`, hit the `/api/*` surfaces, deploy a rogue stress pod, prove replay determinism. **(This is the part the prior AI never ran.)**

## The 11 corpus gates (Phase 2)

| `just` recipe | Certifies | Scenario |
|---|---|---|
| `event-detection-gate` | authored events fire; unresolved/unauthored NEVER upgrade | S3 |
| `app-slo-gate` | declared SLO fires; undeclared never fabricates a bar | S4 |
| `departure-gate` | band-anomaly FP defense — 0 false departures on decoys (CARDINAL) | S6 |
| `transitive-chain-gate` | MEASURED transitive chains; independent faults never merge | S8 |
| `projected-transitive-gate` | PROJECTED multi-hop; band WIDENS per hop, never collapses | S8 |
| `xsvc-gate` | cross-service cascade (MEASURED) | S8 |
| `xsvc-projected-gate` | anticipatory projected cascades (lead time) | S8 |
| `validate-claim-gate` | referee: FALSE-BLOCK == 0, recall ≥ 0.90 | S9 |
| `mcp-gate` | MCP read-only silence/advisory determinism | — |
| `incident-gate` | cross-run incident recurrence, restart-invariant | — |
| `events-gate` | discrete k8s Event JOIN by CEI | — |

> **Important nuance (see file 04):** these 11 gates are **not equally rigorous**. Six are backed
> by an always-on Go drift guard that re-runs the real engine; the other five
> (`xsvc`, `xsvc-projected`, `events`, `mcp`, `incident`) are weaker — fixture-only at run time or
> guarded only by skip-only regen tests. TESTING.md presents all 11 as equal "mathematical proof,"
> which oversells the five.

## Full `just` recipe reference (v3)

Test/build: `test` (go race), `ci` (lint+gen-check+test), `clockd-test`, `harness-test`,
`build`, `gen-check`. Gates: the 11 above. Cluster: `up` (kind — **needs Docker, broken on this
machine**), `down`, `boutique`, `rbac`. Run: `obsd`, `replay`, `clockd`, `console-dev`.
Forecasting model conformance (heavy, opt-in): `clockd-model-conformance`.
