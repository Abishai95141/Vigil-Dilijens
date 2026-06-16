# app-slo gate — evidence (doc 15 cap. A — the application-signal keystone)

**Verdict: PASSED** (`just app-slo-gate`, exit 0) — 2026-06-16.

## What it certifies

An application's own `/metrics` gauge (queue depth) crossing its **customer-declared
SLO** produces a **MEASURED** finding (`PHEN_APP_QUEUE_SATURATION`), and an **undeclared**
SLO never fabricates a bar. This is the keystone that makes application-level numbers
(L1 load / L4 queue / L5 latency / L6 freshness) observable — the layer Vigil was
structurally blind to. The first phenomenon is **L4: aggregation queue saturation**.

Each scenario folds the **REAL** path: `binding.Compile` (SLO resolution from declared
config) → `observe.Materialize` (the gauge laddered vs the declared bar) → `detect.Matcher`
(fires iff crossed), graded against a **LABEL ORACLE** fixed by construction (fire iff the
SLO is declared AND the queue crosses it).

## Floors (all green)

| Floor | Result |
|---|---|
| **DETECTION-FIDELITY** | OK — a finding appears iff the oracle's `expectFire`; full quality |
| **NO-FABRICATION == 0** | 0 — the `undeclared-high-queue` scenario (queue 1500, no declared SLO) produces ZERO findings: an undeclared bar is never fabricated from traffic (the charter ban on learned/default capacity, proven through the real binding) |
| **BORROWED-BAR** | OK — the firing bar is config-sourced (`BarFlagged=false`): the customer's own SLO, never a flagged default |
| **CHARTER == 0** | 0 — no finding restates a cause |

## Scenarios (corpus/app-slo/)

`over-slo` (1500 > SLO 1000 → fires) · `under-slo` (500 < 1000 → silent) ·
`undeclared-high-queue` (1500, no SLO → silent, **the charter floor**) ·
`healthy-no-stream` (SLO declared, no series → silent).

## Machinery

- **Ingestion (A1):** `identity.FamilyApp` (identity from the scrape-target pod, never labels), `kube.ProxyFetcher.PodMetrics` (pods/proxy), `observe.FetchPodMetrics`.
- **SLO binding (A2):** `binding.bindPod` resolves the bar from `PodConfig.SLOs` (read from `vigil.io/slo.*` annotations); undeclared → unbounded; `graph.knownConfigPath` accepts the `slo.*` family.
- **Detection (A3):** the overlay loader gained `signals:` + `members:` blocks (add app signal nodes + structured members the matcher reads); `ontology/graph/overlays/experimental/app-conditions-v1.yaml` authors `SIG_app_queue_depth` + `PHEN_APP_QUEUE_SATURATION` + `THR_APP_QUEUE_DEPTH` + the entity-local check. Loaded behind `--app-metrics-enabled` → released graph hash UNCHANGED (experimental dir).
- Regen: `REGEN_APPSLO_CORPUS=1 go test ./obsd/internal/detect -run RegenAppSLOCorpus`.
- Always-on Go guard: `TestAppSLOCorpusFrozenConsistent` (frozen-corpus drift) + the A3 detection unit tests + the A2 binding unit tests + the A1 identity/ingest tests.
- Scorer: `harness/src/harness/app_slo_gate.py`; regression tests `harness/tests/test_app_slo_gate.py` (6 — good corpus passes + one mutation per floor).
- Recipe: `just app-slo-gate` (Go -race + Python).

## LIVE-VERIFIED on kind-vigil (2026-06-16) — A5

`obsd --app-metrics-enabled` + a synthetic `aggregation` Deployment in ns `traffic`
exposing `/metrics` (`app_queue_depth 1500`) with `prometheus.io/scrape: "true"` and
`vigil.io/slo.queue.max_depth: "1000"`:

- **Scraped**: ingest cycle reports `app:1` — the app `/metrics` endpoint discovered via
  the prometheus.io/scrape annotation and fetched through pods/proxy, attributed to the
  aggregation pod's CEI.
- **Detected (positive)**: `PHEN_APP_QUEUE_SATURATION` fires on `aggregation-…` (full, 1/1)
  in `/api/findings` AND `/api/insights`, member `app_queue_depth` `state=well-above`,
  **`barFlagged=false`** (config-sourced — the customer's declared 1000, not a default),
  the authored note attached. The FIRST application-level phenomenon Vigil detects — the
  L4 link of the smart-traffic chain, previously structurally blind.
- **Charter floor (negative), live**: removing the `vigil.io/slo.queue.max_depth`
  annotation → the pod re-binds **`unbounded: no early-warning eligibility`** (coverage:
  `THR_APP_QUEUE_DEPTH configBound=0 unbounded=32`) → the finding goes **stale** (active /
  non-stale count = **0**), *even though the queue is still 1500*. No declared SLO ⇒ no
  active finding, no fabricated bar — borrowed normativity proven end-to-end on a real cluster.
- **Non-gating**: `--app-metrics-enabled` off ⇒ obsd byte-identical (full `go test -race
  ./obsd/...` green; the released graph hash + the binding/detection digest unchanged).

## Honest scope

The first phenomenon is queue saturation (single-series gauge vs a declared SLO). The
same machinery extends to L1 request-rate-vs-capacity and (with a `now − last_update`
derivation) L6 freshness — follow-up authoring, same gate. **L5 latency** works only where
a DB exporter exposes a scalar p99 gauge (histograms are skipped). The lane proves
**freshness, not correctness** (an on-time-but-wrong value stays out of reach — doc 15 §6).
