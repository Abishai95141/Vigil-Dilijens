# app-slo gate — evidence (doc 15 cap. A — the application-signal lane: L4 + L6 + L1)

**Verdict: PASSED** (`just app-slo-gate`, exit 0) — 2026-06-16.

## What it certifies

An application's own `/metrics` signal crossing its **customer-declared SLO** produces a
**MEASURED** finding, and an **undeclared** SLO never fabricates a bar. This is the lane that
makes application-level numbers observable — the layer Vigil was structurally blind to. It
now spans **three links of the smart-traffic chain**, each its own phenomenon:

| Link | Phenomenon | Signal → bar | Derivation |
|---|---|---|---|
| **L4 queue** (keystone) | `PHEN_APP_QUEUE_SATURATION` | `app_queue_depth` gauge vs `slo.queue.max_depth` | gauge level |
| **L6 freshness** (differentiator) | `PHEN_APP_DATA_STALENESS` | `app_last_update_seconds` epoch vs `slo.freshness.max_age` | **age-from-timestamp** (evalNow − value, injected clock) |
| **L1 load** (trigger) | `PHEN_APP_LOAD_SURGE` | `app_requests_total` counter vs `slo.requests.max_rate` | **counter→rate** (Δ/Δt, reset-aware) |

Each scenario folds the **REAL** path: `binding.Compile` (SLO resolution from declared
config) → `observe.Materialize` (the metric laddered vs the declared bar, incl. the two
derivations) → `detect.Matcher` (fires iff crossed), graded against a **LABEL ORACLE** fixed
by construction (fire iff the SLO is declared AND the metric crosses it).

The L6 age transform is **MEASURED** and **replay-deterministic**: age = `evalNow.Unix() −
value` uses the INJECTED eval clock (never `time.Now`), so same readings + same evalNow ⇒
same age — a deterministic arithmetic consequence of two facts, exactly like a counter rate.
A future timestamp yields a negative age (never fires); a hung app freezes its last-update
while evalNow advances, so the age GROWS and the staleness is caught.

## Floors (all green)

| Floor | Result |
|---|---|
| **DETECTION-FIDELITY** | OK — for each scenario, a finding for its phenomenon appears iff the oracle's `expectFire`; full quality |
| **NO-FABRICATION == 0** | 0 — every `*-undeclared` scenario (crossing metric, NO declared SLO) produces ZERO findings across all three families: an undeclared bar is never fabricated (the charter ban on learned/default capacity, proven through the real binding) |
| **BORROWED-BAR** | OK — every firing bar is config-sourced (`BarFlagged=false`): the customer's own SLO, never a flagged default |
| **NO-CROSS-TALK == 0** | 0 — a scenario exercising one phenomenon never lights up another app phenomenon (the three signals are independent: a stale timestamp is not a deep queue is not a load surge) |
| **CHARTER == 0** | 0 — no finding restates a cause |

## Scenarios (corpus/app-slo/, 10 total)

- **L4 queue:** `over-slo` (1500 > 1000 → fires) · `under-slo` (500 < 1000 → silent) ·
  `undeclared-high-queue` (1500, no SLO → silent, **charter floor**) · `healthy-no-stream`
  (SLO declared, no series → silent).
- **L6 freshness:** `freshness-stale` (age 120s > 30s → fires) · `freshness-fresh`
  (age 8s < 30s → silent) · `freshness-undeclared` (age 120s, no SLO → silent, **charter floor**).
- **L1 load:** `load-over` (150/s > 100/s → fires) · `load-under` (40/s < 100/s → silent) ·
  `load-undeclared` (150/s, no SLO → silent, **charter floor**).

The 4 queue scenarios reproduce **byte-identically** (modulo the new lane graph hash) — adding
L6/L1 did not perturb the L4 keystone (proven by the Go drift guard + git diff).

## Machinery

- **Ingestion (A1):** `identity.FamilyApp` (identity from the scrape-target pod, never labels), `kube.ProxyFetcher.PodMetrics` (pods/proxy), `observe.FetchPodMetrics`.
- **SLO binding (A2):** `binding.bindPod` resolves the bar from `PodConfig.SLOs` (read from `vigil.io/slo.*` annotations); undeclared → unbounded; `graph.knownConfigPath` accepts the `slo.*` family.
- **Detection (A3):** the overlay loader gained `signals:` + `members:` blocks (add app signal nodes + structured members the matcher reads); `ontology/graph/overlays/experimental/app-conditions-v1.yaml` authors `SIG_app_queue_depth` + `PHEN_APP_QUEUE_SATURATION` + `THR_APP_QUEUE_DEPTH` + the entity-local check. Loaded behind `--app-metrics-enabled` → released graph hash UNCHANGED (experimental dir).
- Regen: `REGEN_APPSLO_CORPUS=1 go test ./obsd/internal/detect -run RegenAppSLOCorpus`.
- Always-on Go guard: `TestAppSLOCorpusFrozenConsistent` (frozen-corpus drift) + the A3 detection unit tests + the A2 binding unit tests + the A1 identity/ingest tests.
- Scorer: `harness/src/harness/app_slo_gate.py`; regression tests `harness/tests/test_app_slo_gate.py` (6 — good corpus passes + one mutation per floor).
- Recipe: `just app-slo-gate` (Go -race + Python).

## LIVE-VERIFIED on kind-vigil (2026-06-16) — A5 (L4) + the L6/L1 chain completion

`obsd --app-metrics-enabled` + a synthetic `aggregation` Deployment in ns `traffic`
exposing all three signals on `/metrics` (`app_queue_depth 1500`, `app_last_update_seconds`
= a fixed epoch 120s in the past, `app_requests_total` rising at ~150/s) with
`prometheus.io/scrape: "true"` and the three SLO annotations
(`vigil.io/slo.queue.max_depth: "1000"`, `vigil.io/slo.freshness.max_age: "30"`,
`vigil.io/slo.requests.max_rate: "100"`):

- **Scraped**: ingest cycle reports `app:1` — the app `/metrics` endpoint discovered via the
  prometheus.io/scrape annotation, fetched through pods/proxy, attributed to the aggregation
  pod's CEI. The counter genuinely increments between scrapes (rate measured live), the
  timestamp is aged against the eval clock.
- **Detected (positive), all three full quality on `aggregation-…`** in `/api/findings` +
  `/api/insights`, each `state=well-above`, **`barFlagged=false`** (config-sourced):
  - `PHEN_APP_QUEUE_SATURATION` — `app_queue_depth` (L4).
  - `PHEN_APP_DATA_STALENESS` — `app_last_update_seconds`, the data age past the freshness
    SLO (L6, the differentiator). The age transform ran live off the injected eval clock.
  - `PHEN_APP_LOAD_SURGE` — `app_requests_total` rate-converted past the capacity SLO (L1,
    the chain trigger).
- **Charter floor at scale, live**: each app rule binds `configBound=1, unbounded=31` — ONLY
  the annotated aggregation pod gets a bar; the other 31 pods (boutique, kube-system, …) have
  all three rules instantiated but **unbounded**, firing nothing. 31 live negatives = no
  fabricated bar anywhere a SLO is undeclared.
- **Charter floor dynamic, live (the L6 path)**: removing ONLY the
  `vigil.io/slo.freshness.max_age` annotation → `THR_APP_DATA_AGE` re-binds
  `configBound=0 unbounded=32` → `PHEN_APP_DATA_STALENESS` goes inactive **even though the
  data is still 120s+ stale**, while `PHEN_APP_QUEUE_SATURATION` and `PHEN_APP_LOAD_SURGE`
  (SLOs intact) keep firing. No declared SLO ⇒ no active finding, no fabricated freshness
  floor — and the de-activation is SPECIFIC to the removed bar, not a global outage.
- **Non-gating**: `--app-metrics-enabled` off ⇒ obsd byte-identical (full `go test -race
  ./obsd/...` green, 20 pkgs; the RELEASED graph hash unchanged — the app overlay is
  experimental/flag-gated, loaded via `LoadWithExtraOverlays` only when the lane runs).

## Honest scope

Three phenomena now: queue depth (gauge level), data staleness (age-from-timestamp), request
load (counter→rate) — each a single app series vs a declared SLO. **L5 latency** works only
where a DB exporter exposes a scalar p99 gauge (histograms are skipped). The lane proves
**freshness, not correctness** (an on-time-but-wrong value stays out of reach — doc 15 §6).
Each phenomenon reports what IS, entity-local; **relating them into a chain reaction is
Capability B's authored, transitive job — never inferred here** (the no-cross-talk floor
proves they are independent until B stitches them).
