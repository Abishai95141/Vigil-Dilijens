# ABB Ability Genix simulation — scenarios & ground truth

The ABB-aligned reference workload (`deploy/workloads/abb-genix/`): an *ABB Ability
Genix*–style predictive-maintenance pipeline for a water/wastewater pumping plant.
This file is the **label oracle** for its failure scenarios — what should fire, on
which CEI, when — to grade a live run against. Companion to the deploy README.

> **Status:** authored, not yet live-certified on kind. Verify per the plan's
> verification section (`just abb-genix` → run `obsd` with `--app-metrics-enabled
> --ksm-enabled --events-enabled` → trigger each rig → confirm the expected
> phenomenon + CEI). Until then these are the *expected* outcomes, not a PASSED gate.

## Topology (what binds)

10 workloads, 4 PVCs, all pinned to `vigil-worker`. The MEASURED dependency path:

```
smart-sensors ─poll─▶ opcua-gateway ─MQTT─▶ edgenius-broker(Mosquitto,PVC)
   ─sub─▶ stream-processor ─write─▶ genix-historian(InfluxDB,PVC) ─batch─▶ genix-datalake(MinIO,PVC)
pdm-analyzer ─query historian─▶ ─findings─▶ asset-registry(Postgres,PVC) ◀─read─ asset-api
operations-dashboard(Grafana) ─query─▶ genix-historian + asset-api
```

Edges Vigil should bind: `selects` (each of 9 Services → its pods), `mounts`
(edgenius-broker, genix-historian, genix-datalake, asset-registry → their 4 PVCs),
`runs-on` (all → the sim node), and the observed-flow / authored dependency chain
above (the `operations-dashboard → genix-historian / asset-api` edges are made standing
by an always-on control-room "viewer" sidecar). Two deliberate `doc 14 §3.3`
customizations at install (per the `abb-genix` recipe): **opcua-gateway** unbounded
(memory limit stripped → resolvability-hole / Tier-B-ineligible), and **smart-sensors**
tuned near-threshold (96Mi → 64Mi).

## Scenario oracle

| Rig (`corpus/chaos/`) | Inject | Expected phenomenon | CEI (role anchor) | Class |
|---|---|---|---|---|
| `abb-analyzer-leak.yaml` | `/ctl?leak=on` | `PHEN_MEMORY_LEAK` then `PHEN_OOM_KILL_CGROUP` (OOM via the **events lane** on kind — `lastState.terminated.reason=OOMKilled`, exit 137 — needs `--events-enabled`), recognized as the authored `MEMORY_LEAK → OOM_KILL_CGROUP` story; **forecastable** while rising | `pdm-analyzer` (Container) | MEASURED (+PROJECTED band) |
| `abb-connectivity-loss.yaml` | `/ctl?disconnect=1` | `PHEN_APP_DATA_STALENESS` on asset-api, surfaced along the gateway→…→asset-api chain | `asset-api` (Role) | MEASURED |
| `abb-broker-backpressure.yaml` | gateway `rate=20` + stream `slow=300` + stream `cpuburn=1` | `PHEN_APP_QUEUE_SATURATION` (L4) **and** `THROTTLING_CASCADE` (real CPU burn vs the 250m limit); `PROBE_FAILURE_RESTART` only if sustained throttle starves `/healthz` (not guaranteed) | `stream-processor` (Role/Container) | MEASURED |
| `abb-load-surge.yaml` | gateway `rate=80` | `PHEN_APP_LOAD_SURGE` (L1) — `rate(app_requests_total) > 200` | `opcua-gateway` (Role) | MEASURED |
| `abb-historian-diskfill.yaml` | gateway `rate=30` | `PHEN_DISK_FILLING` — **gate-pending on kind** (no per-volume `kubelet_volume_stats`; see rig header). Kept under the 200/s bar so it does not also trip LOAD_SURGE | `genix-historian` (PVC) | MEASURED |

## Signal → bar map (matches the app-slo lane; see `app-slo-gate.md`)

| Phenomenon | Metric (label-free) | Declared bar (annotation) | Derivation |
|---|---|---|---|
| `PHEN_APP_QUEUE_SATURATION` | `app_queue_depth` gauge | `vigil.io/slo.queue.max_depth: "500"` | gauge level |
| `PHEN_APP_DATA_STALENESS` | `app_last_update_seconds` epoch | `vigil.io/slo.freshness.max_age: "120"` | age = evalNow − value (injected clock) |
| `PHEN_APP_LOAD_SURGE` | `app_requests_total` counter | `vigil.io/slo.requests.max_rate: "200"` | counter→rate (Δ/Δt, reset-aware) |

All bars are the **customer's own declared config** (borrowed normativity) — none
learned. cAdvisor container metrics (memory working-set, CPU, throttle, OOM events)
are scraped automatically for all 10 pods, so the leak/OOM/throttle phenomena need no
app instrumentation.

## Honest caveats

- **Single node:** the whole sim is pinned to one worker for a single-node *workload*
  feel; **node-level** pressure phenomena (the 2-hop noisy-neighbour walk, node memory/IO
  pressure) are NOT representable here and are out of scope — the scenarios are
  container-scoped, app-SLO, and cross-service-cascade, all of which ARE representable.
- **Historian disk-fill:** gate-pending on kind (local-path emits no per-volume stats).
  Real on the k3s-on-VMs staging cluster; labeled honestly, not claimed.
- **Charter:** the workload authors nothing into Vigil. It only emits signals; every bar
  is a declared `resources.limits` or `vigil.io/slo.*` value.
