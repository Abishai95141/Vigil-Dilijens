# ABB Ability Genix — predictive-maintenance simulation

An **ABB-aligned** reference workload for Vigil: a single-node, multi-pod, multi-service
Kubernetes simulation of an *ABB Ability Genix*–style **predictive-maintenance pipeline**
for a water/wastewater pumping plant. It stands alongside the Online Boutique demo and
gives Vigil a realistic **industrial** dependency graph — pods ↔ services ↔ PVCs — with
ABB-meaningful failure points to detect.

It changes **no `obsd` code**. Vigil discovers it through the standard watch + cAdvisor /
KSM / events / app-metrics lanes. This is a *workload*, not engine code: it authors
nothing into Vigil, declares only the customer's own `resources.limits` / `vigil.io/slo.*`
bars (borrowed normativity), and only **emits** signals.

## The scenario

Pumps, motors and bearings instrumented with **ABB Ability Smart Sensors** (vibration,
bearing temperature, motor current, speed) stream telemetry up the **ISA-95** pyramid
through a **System 800xA / OPC-UA** connectivity gateway, into the **ABB Ability Edgenius**
edge MQTT broker, where it is normalized by **Genix Integrate**, historized in the **Genix**
time-series historian + data lake, analyzed by a **Genix Model Fabric** predictive-maintenance
service (vibration trend → remaining-useful-life), and surfaced on a **Genix** operations
dashboard and asset-performance API. This is the documented ABB Ability flow:
`sensor → edge → broker → historian → analytics → dashboard/alert`.

## Components (ISA-95 → ABB product → K8s)

| Workload | ABB product it represents | Image | Stateful / PVC |
|---|---|---|---|
| `smart-sensors` | ABB Ability **Smart Sensors** (L1 field) | `vigil-abb-sim` (ROLE=sensors) | — |
| `opcua-gateway` | System **800xA** / OPC-UA→MQTT (L2) | `vigil-abb-sim` (ROLE=gateway) | — |
| `edgenius-broker` | ABB Ability **Edgenius** broker (edge) | `eclipse-mosquitto:2` | **PVC** 1Gi |
| `stream-processor` | **Genix Integrate** normalization (L3) | `vigil-abb-sim` (ROLE=stream) | — |
| `genix-historian` | **Genix** time-series historian (L3) | `influxdb:2.7` | **PVC** 10Gi |
| `genix-datalake` | **Genix** data fabric / object store (L3) | `minio/minio` | **PVC** 10Gi |
| `pdm-analyzer` | **Genix Model Fabric** PdM (L3) | `vigil-abb-sim` (ROLE=analyzer) | — |
| `asset-registry` | **Genix** asset/APM store (L3) | `postgres:16` | **PVC** 2Gi |
| `asset-api` | **Genix** Asset Performance Mgmt API (L3) | `vigil-abb-sim` (ROLE=assetapi) | — |
| `operations-dashboard` | **Genix** operations dashboard (L3 ops view) | `grafana/grafana:11` (+ viewer sidecar) | — |

> ABB-product mappings are *representative* of where each piece sits in the ABB Ability /
> System 800xA stack, not claims that the named ABB software runs — Mosquitto stands in
> for the Edgenius edge broker, the OPC-UA→MQTT gateway for an 800xA connectivity server,
> etc. No OPC-UA/800xA software actually executes; the value is the realistic *shape*.

**Hybrid:** the stateful backbone is real off-the-shelf images (so PVCs and the MQTT/DB
data path are genuine); the ABB-specific layers are one small role-parameterized sim
image (`sim/`) that emits the exact metrics Vigil wants and exposes a `/ctl` control
surface for deterministic failure injection.

## Dependency map

```
smart-sensors ─poll─▶ opcua-gateway ─MQTT─▶ edgenius-broker(PVC)
   ─sub─▶ stream-processor ─write─▶ genix-historian(PVC) ─batch─▶ genix-datalake(PVC)
pdm-analyzer ─query─▶ genix-historian ─findings─▶ asset-registry(PVC) ◀─read─ asset-api
operations-dashboard ─query─▶ genix-historian + asset-api
```

## Run it

**Option A — onto the existing 3-node `vigil` cluster** (pinned to one worker; shares the
cluster with Online Boutique):
```bash
just up                       # if not already up
just rbac
just abb-sim-build            # build vigil-abb-sim:0.1, load into the `vigil` cluster
just abb-genix                # deploy (auto-labels a worker) + doc 14 §3.3 customizations
just abb-genix-gate           # smoke gate: pods Ready, 4 PVCs Bound, data flowing
```

**Option B — onto a DEDICATED single-node cluster** (full isolation, true single-node):
```bash
just abb-up                   # create the single-node `vigil-abb` cluster
just rbac
just abb-sim-build vigil-abb  # build + load into `vigil-abb`
just abb-genix
just abb-genix-gate
```

Then observe with the app lanes on:
```bash
just obsd -- --app-metrics-enabled --ksm-enabled --events-enabled   # (flags per main.go)
```

The node-pinning is cluster-agnostic: `abb-genix` labels whichever schedulable node it
finds with `vigil.io/sim-node=abb-genix`, and every pod selects that label — so the same
manifests deploy unchanged on either cluster.

Grafana (the operations dashboard) provisions a **Plant Overview** (vibration + bearing
temperature per asset from the historian); reach it with
`kubectl -n abb-genix port-forward svc/operations-dashboard 3000:3000`. The
stream-processor archives NDJSON batches to the **genix-datalake** (MinIO) every 30s, and
a control-room **viewer** sidecar continuously polls the historian + asset-api — so both
of those dependency edges carry real, standing traffic (not browser-gated).

Apply at install (mirrors the boutique discipline): **opcua-gateway** is left memory-
**unbounded** (resolvability-hole → Tier-B-ineligible); **smart-sensors** is tuned
**near-threshold** (96Mi → 64Mi; measured working set ~14Mi). Tear down with
`just abb-genix-down` (namespace) or `just abb-down` (the dedicated cluster).

## Failure rigs (`corpus/chaos/abb-*.yaml`)

Each rig is a Job that drives a sim's `/ctl` surface. Ground truth + expected phenomena
are in [`corpus/labels/abb-genix-scenarios.md`](../../../corpus/labels/abb-genix-scenarios.md).

| `kubectl apply -f corpus/chaos/…` | Incident | Expect |
|---|---|---|
| `abb-analyzer-leak.yaml` | PdM analytics OOM | `MEMORY_LEAK` → `OOM_KILL_CGROUP` on `pdm-analyzer` (+ forecast) |
| `abb-connectivity-loss.yaml` | field link lost | `APP_DATA_STALENESS` on `asset-api` along the chain |
| `abb-broker-backpressure.yaml` | broker backpressure | `APP_QUEUE_SATURATION` (+ throttle) on `stream-processor` |
| `abb-load-surge.yaml` | telemetry surge | `APP_LOAD_SURGE` on `opcua-gateway` |
| `abb-historian-diskfill.yaml` | historian disk fill | `DISK_FILLING` — **gate-pending on kind** (see rig header) |

Heal commands are in each rig's header. Tear down with `just abb-genix-down`.

## Vigil compatibility contract (what obsd reads, and what breaks it)

This is the **single source of truth** for the Vigil-facing surface. Everything else in
the config is ordinary workload config. obsd reads *only* these; changing any of them
moves a detection result, and most fail **silently** (no error, pod stays Ready).

| Touchpoint | Where | obsd uses it for | If you change it |
|---|---|---|---|
| `prometheus.io/scrape: "true"` (+`port`,`path`) | `20-sims.yaml` pod annotations | opt-in app-metrics scrape (needs `--app-metrics-enabled`) | non-`"true"` or wrong `port` → scrape silently off |
| **metric name** `app_requests_total` ⇄ `vigil.io/slo.requests.max_rate` ⇄ overlay rule | `sim.py` `build_metrics()` · `20-sims.yaml` · `…/experimental/app-conditions-v1.yaml` | `PHEN_APP_LOAD_SURGE` bar (counter→rate) | rename **any one** of the three → binding dies silently |
| `app_queue_depth` ⇄ `vigil.io/slo.queue.max_depth` ⇄ overlay rule | same three files | `PHEN_APP_QUEUE_SATURATION` (gauge) | same — change all three together |
| `app_last_update_seconds` ⇄ `vigil.io/slo.freshness.max_age` ⇄ overlay rule | same three files | `PHEN_APP_DATA_STALENESS` (age = evalNow − value, seconds) | same |
| metrics are **label-free** | `sim.py` `/metrics` | exactly 1 stream per `(pod,metric)` or Materialize won't bind | adding a label splits the stream → QA-FAILED (this one is *visible*) |
| `resources.limits.{memory,cpu}` `[VIGIL BAR]` | `20-sims.yaml`, `10-backbone.yaml` | OOM / throttle detection bars (borrowed normativity); **absent = unbounded/Tier-B-ineligible** | moves a threshold; **`just abb-genix` patches gateway→unbounded & sensors→64Mi at install — committed ≠ live for those two** |
| `volumeClaimTemplates` storage | `10-backbone.yaml` | `mounts` edges + DISK_FILLING bar (gate-pending on kind) | changes the disk bar |
| Service `selector` == pod label `app:<name>` | `20-sims.yaml`, `10-backbone.yaml` | `selects` (svc→pod) edges | mismatch → topology edge missing |
| **Required obsd flags** | `just obsd -- …` | `--app-metrics-enabled` (SLO bars), `--ksm-enabled` (restarts/PVC), `--events-enabled` (OOM on kind) | without them the annotations are **inert** |

**Not** read by obsd (despite the `vigil.io/` prefix): `vigil.io/sim-node` (scheduling pin)
and `vigil.io/workload` (decorative). A new `vigil.io/slo.*` key is **inert** until a
matching rule exists in the `experimental/` overlay (itself flag-gated). The three-way
metric⇄key⇄overlay agreement is asserted live by `just abb-genix-gate`.

## The sim image (`sim/`)

One image, five roles via `ROLE` (`sensors|gateway|stream|analyzer|assetapi`). Each role
exposes a **label-free** `/metrics` (one stream per `(pod, metric)` so Vigil binds),
declares ABB-meaningful `vigil.io/slo.*` bars, and a `/ctl` control surface. Built locally
and `kind load`-ed (no registry); `imagePullPolicy: IfNotPresent`. Not part of `obsd` —
the Go runtime stays CGO-free regardless.
