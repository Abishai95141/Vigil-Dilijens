# ABB Genix — Vigil Failure Simulation Console

A self-serve Streamlit chaos console for the `abb-genix` cluster. Trigger **real,
controlled** failures and watch Vigil interpret them — forecasting, anomaly detection,
multi-event chains, dependency mapping, and root-cause analysis.

> Companion docs: `docs/24` (bring-up + onboarding), `docs/25` (the incident scenarios),
> `docs/17` (the original simulator plan). Prerequisite: the system is up per `docs/24`
> (cluster + obsd on `:9095`). For PVC-fill to be *visible in Vigil* you need a real-storage
> cluster (kind's local-path doesn't emit volume stats — the disk still fills, the metric is dark).

## Run

```bash
just simulator                          # from the repo root
# or:
cd simulator && uv run streamlit run app.py
```

Opens at http://localhost:8501. The sidebar shows live cluster + Vigil status and the
**HEAL ALL / RESET** button.

## What it is (and what it is not)

- **Transparent.** Every control states *what* it does to the cluster, the *downstream
  effect*, which *Vigil capability* it tests, the *expected phenomenon*, and shows the exact
  `kubectl` command. Nothing is a mock — each action is a genuine `kubectl` operation run
  through your existing kubeconfig/context (no in-cluster agent, no special RBAC).
- **Reversible.** Every fault has a **Heal**; the sidebar **HEAL ALL / RESET** returns the
  namespace to a clean baseline (clears sim flags, restores patched limits, removes disk
  ballast, scales everything back to 1).

## Layout

| Tab | Purpose |
|---|---|
| 🎛️ Control Panel | the fault catalog by class — Application (sim `/ctl`), Resource (limit patches), Storage (PVC fill), Lifecycle (kill/flap), Communication (broker/DB outage). Inject / Heal each. |
| 🚀 Scenarios | one-button, multi-fault presets (see below). |
| 📊 Vigil Live | how Vigil currently sees the cluster (detection, forecast, cross-service cascade, incidents) — read from obsd `/api`. |
| 🩺 Cluster | live pod table, the armed-fault state per sim pod, and the dependency graph. |

## Fault catalog (real mechanisms)

| Fault | Mechanism | Vigil capability |
|---|---|---|
| Memory leak | `pdm-analyzer /ctl?leak=on` | forecasting → MEMORY_LEAK → OOM cascade |
| Latency / queue | `stream-processor /ctl?slow=<ms>` | QUEUE_SATURATION, degradation |
| CPU burn | `stream-processor /ctl?cpuburn=1` | THROTTLING_CASCADE |
| Load surge | `opcua-gateway /ctl?rate=<n>` | LOAD_SURGE |
| Connectivity loss | `opcua-gateway /ctl?disconnect=1` | staleness chain, root-cause honesty |
| CPU squeeze | patch CPU limit down | throttling / DB bottleneck |
| Memory squeeze | patch memory limit down | OOM / restarts |
| PVC disk fill | `dd` ballast into a stateful PVC | storage failure (metric dark on kind) |
| Kill pod | `kubectl delete pod` | restart, churn-stable identity |
| Replica instability | `kubectl scale` flap | replica churn |
| Message-bus outage | scale `edgenius-broker` → 0 | comms breakdown, propagation |
| Database outage | scale `genix-historian`/`asset-registry` → 0 | dependency, multi-service failure |

## One-button scenarios

| Scenario | What it arms |
|---|---|
| 🌋 Full Cascade — Plant Outage | connectivity loss + memory leak + latency + CPU burn (all capabilities at once) |
| 🔥 Resource Exhaustion Sweep | memory + CPU + disk together |
| 🗄️ Dependency Breakdown | historian outage → fan-out to all its callers |
| 🔁 Replica Instability & Crashes | replica flap + pod kill |
| 〰️ Intermittent Degradation | oscillating latency (background thread) |

## Validated end-to-end (app-driven, against live Vigil)

The **Full Cascade — Plant Outage** scenario was launched from this app's button and validated
in Vigil (obsd `/api`):

| Capability | Evidence (from the app-driven run) |
|---|---|
| Forecasting | PROJECTED early-warning on `Deployment/pdm-analyzer` `container_memory_working_set_bytes` → 243.2Mi **config** bar, 180–420s lead, **wide** band, precursor `PHEN_OOM_KILL_CGROUP` |
| Anomaly detection | `PHEN_THROTTLING_CASCADE` findings + 233 onsets (C2) |
| Multi-event chaining / propagation | `PHEN_APP_DATA_STALENESS` on asset-api → cross-service cascade to operations-dashboard |
| Dependency mapping | cascade `asset-api → operations-dashboard` over a MEASURED observed-flow edge |
| Root-cause analysis | root = asset-api (visible), authored `UPSTREAM_DEGRADATION→DOWNSTREAM_IMPACT` why; the **gateway was NOT blamed** (honest restraint) |

The app's **Vigil Live** tab showed it in real time (Active findings 4 · Forecast cards 1 ·
Cross-service ● active · Onsets 233). Healed via **HEAL ALL / RESET**.

> **Honest finding from the run:** an earlier Full Cascade combined a gateway *connectivity
> loss* with the stream-processor *latency* fault — and the connectivity loss **starved the
> queue** (no messages flow → no backlog builds), so `QUEUE_SATURATION` could not fire. The
> preset was corrected to use a **historian DB outage** instead (broker keeps flowing, so the
> queue still builds *and* asset-api goes stale). A real emergent interaction, surfaced and fixed.

## Module map

- `app.py` — the Streamlit UI.
- `faults.py` — the transparent fault catalog + inject/heal engine (the single source of truth).
- `scenarios.py` — one-button presets + global reset (composes `faults`).
- `kube.py` — transparent `kubectl` wrappers (each returns the exact command it ran).
- `vigil.py` — read-only obsd `/api` + `/mcp` client (never writes to Vigil).
