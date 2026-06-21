# Cloud Single-Node Test Plan — stress-testing Vigil on a real cluster

**Status:** plan / instructions (no code changed yet). Reference this while you stand up
the cloud environment.
**Goal:** run **one** single-node Kubernetes cluster **on a cloud provider**, deploy a
**real-time load generator with interconnected pod dependencies + injected failures**
(load surge, connectivity loss, queue backpressure, PVC/disk fill, memory-leak→OOM), run
**Vigil (`obsd`)** against it, and **watch Vigil detect/forecast in real time.**

> TL;DR — **Yes, the existing single-node setup is the right base. We adapt it, we do not
> rebuild it.** And **yes, we already have a proper load generator** (the ABB-Genix
> simulator + the `corpus/chaos/abb-*.yaml` failure rigs). Only three things genuinely
> change going to cloud, and all three are small.

---

## 0. Short answers to your two questions

**"Is the existing single-node cluster fine to adapt?"** — Yes.
- `just abb-up` already builds a **true single-node** cluster (`deploy/kind/abb-cluster.yaml`
  is a one-node cluster).
- `just abb-genix` deploys a realistic **industrial dependency graph** (pods ↔ services ↔
  PVCs) onto whatever node it finds — it auto-labels the node, so the *same manifests* run
  on kind locally **or** on a cloud node with **no edits**.
- The only thing that does not carry over is *how the cluster is created* and *how the one
  custom image gets onto it*. Everything else (manifests, RBAC, chaos rigs, the gate) is
  reused as-is.

**"Do we have a proper load generator for cloud testing?"** — Yes, and it's better-suited
than a generic HTTP bencher. See [§2](#2-the-load-generator-we-already-have). In one line:
the **ABB-Genix simulator** (`deploy/workloads/abb-genix/sim/sim.py`) produces continuous,
real, interconnected pipeline traffic, and exposes a `/ctl` surface that the
`corpus/chaos/abb-*.yaml` Jobs drive to inject each failure deterministically.

---

## 1. What the local environment looks like today (the base we adapt)

| Piece | File / recipe | What it is |
|---|---|---|
| Single-node cluster | `deploy/kind/abb-cluster.yaml` · `just abb-up` | 1 control-plane node, schedulable. |
| The workload | `deploy/workloads/abb-genix/{00,10,20}-*.yaml` · `just abb-genix` | ABB Ability Genix–style predictive-maintenance pipeline (water plant). |
| The custom image | `deploy/workloads/abb-genix/sim/` · `just abb-sim-build` | One role-parameterized sim (`vigil-abb-sim:0.1`), 5 roles. |
| Read-only RBAC | `deploy/rbac/clusterrole.yaml` · `just rbac` | `get/list/watch` + `nodes/proxy` (cAdvisor). Vigil only **watches**, never reconciles. |
| Smoke gate | `just abb-genix-gate` | Asserts pods Ready, 4 PVCs Bound, data flowing, and the Vigil metric⇄SLO⇄overlay contract. |
| The failure rigs | `corpus/chaos/abb-*.yaml` | 5 chaos Jobs (load/connectivity/backpressure/leak/diskfill). |
| Vigil itself | `obsd` binary · `just obsd -- <flags>` | Runs **out-of-cluster** (`--kubeconfig`) or in-cluster (`--in-cluster`); console/API + health on `:9095`. |

**The dependency graph the sim builds** (this is the "interconnected pods" you asked for):

```
smart-sensors ─poll─▶ opcua-gateway ─MQTT─▶ edgenius-broker (PVC 1Gi)
   ─sub─▶ stream-processor ─write─▶ genix-historian (PVC 10Gi) ─batch─▶ genix-datalake (PVC 10Gi)
pdm-analyzer ─query─▶ genix-historian ─findings─▶ asset-registry (PVC 2Gi) ◀─read─ asset-api
operations-dashboard ─query─▶ genix-historian + asset-api   (+ a viewer sidecar polling continuously)
```

Real backends (Mosquitto, InfluxDB, MinIO, Postgres, Grafana) → the PVCs and the data path
are **genuine**, not decoration. That's exactly the "different connections and interconnected
pod dependencies" you wanted to stress.

---

## 2. The load generator we already have

You do **not** need to build a load generator. The sim **is** one, plus a failure injector.
Here is exactly what it produces and what each knob does (all verified in `sim.py`):

**Standing real-time traffic (no chaos applied):**
- sensors emit fresh readings every **1s**;
- gateway polls + forwards at `sample_rate` (default **5/s × 5 assets**);
- stream drains the MQTT queue into InfluxDB continuously;
- a flush archives NDJSON batches to MinIO every **30s**;
- the analyzer runs a trend→RUL model every **10s** and writes Postgres;
- asset-api refreshes every **5s**; a viewer sidecar polls historian + api non-stop.

So even at idle the cluster has **real, multi-hop, cross-pod traffic** flowing the whole time.

**The `/ctl` control surface — deterministic failure injection (this is the generator's "load profile" knobs):**

| Inject (`GET /ctl?…`) | On role | Effect | Vigil should surface |
|---|---|---|---|
| `rate=80` | gateway | ramps forward rate (≈400 samples/s vs a 200 bar) | `PHEN_APP_LOAD_SURGE` (MEASURED rate crossing) |
| `disconnect=1` | gateway | drops the field link; freshness freezes downstream | `PHEN_APP_DATA_STALENESS` along the chain |
| `slow=<ms>` | stream | injects per-message latency; queue builds | `PHEN_APP_QUEUE_SATURATION` |
| `cpuburn=1` | stream | genuine CPU spin past the CPU limit | CFS `THROTTLING_CASCADE` |
| `degrade=<asset>` | sensors | bearing vibration climbs (realistic fault) | analyzer alerts / RUL drop |
| `leak=on` | analyzer | ~4 MiB/pass growth to the limit | `MEMORY_LEAK` → `OOM_KILL_CGROUP` (the flagship) |

**The chaos rigs (`corpus/chaos/abb-*.yaml`) are just Jobs that call those knobs** with the
right values + heal commands. Ground truth + expected phenomena are written in
`corpus/labels/abb-genix-scenarios.md`. **PVC/disk failure** is `abb-historian-diskfill.yaml`
(fills the historian's 10Gi PVC) — note this one is *gate-pending on kind* but **will work on
a real cloud disk** (see [§7](#7-why-cloud-actually-adds-value)).

> Honest scope note: this is a **domain-realistic** load generator (interconnected pipeline +
> targeted faults), **not** a raw-RPS HTTP bench like k6/Locust. For "interconnected pod
> dependencies, test failures, PVC failures" it is exactly the right tool. If you ever
> separately want to hammer the kube-apiserver with raw QPS, that's a different tool and a
> different goal — don't conflate the two.

---

## 3. Cloud cluster choice

**Primary recommendation: k3s on a single cloud VM.** This is the cheapest, closest-to-local
option, and it's already the documented next step — `deploy/CLAUDE.md` literally says *"stand
up the k3s-on-VMs staging cluster."* (The dev machine itself already runs k3s, so this isn't
new ground.) k3s ships the **same default storage provisioner** (rancher `local-path`) that
kind uses, so the PVCs bind with **zero manifest edits**.

> ⚠️ **Storage caveat — read this before you pick a cluster.** `local-path` (kind **and**
> k3s) emits **zero `kubelet_volume_stats`**, so **PVC-fill is unobservable** on it — the
> `abb-historian-diskfill` rig stays invisible, exactly as it does on kind (docs/16:92-94).
> Since you specifically want **PVC failures** tested, you have a choice:
> - **k3s-on-VM with default `local-path`** → cheapest; covers **5 of the 6** scenarios
>   (load, connectivity, backpressure, throttle, leak→OOM) **plus** the *container
>   ephemeral-storage* disk rig (`corpus/chaos/disk-fill-ephemeral.yaml`, observed via
>   cAdvisor, works everywhere). The **PVC-volume** fill (`abb-historian-diskfill`) stays
>   unobservable — honest gap.
> - **Managed 1-node (DOKS / GKE / AKS)** *or* **k3s-on-VM + a real block-storage CSI**
>   (the provider's CSI, or Longhorn) → real volumes **report stats**, so `DISK_FILLING` on
>   the historian PVC finally fires. This is what actually delivers the PVC-failure demo.
>
> **Recommendation:** if the PVC-fill demo matters (it does), use a **managed 1-node cluster
> with block-storage as the default class** — **DigitalOcean DOKS** is the simplest/cheapest
> managed option. If budget is the priority and you can accept the one missing signal, use
> **k3s-on-VM**. Both run the abb-genix manifests unchanged.

| | Primary — k3s on a VM | Fallback — managed 1-node |
|---|---|---|
| What | One Linux VM, `curl -sfL https://get.k3s.io \| sh -` | DigitalOcean DOKS / GKE / EKS / AKS / Civo, 1-node pool |
| Sizing | **4 vCPU / 8 GB RAM / 80 GB disk** (comfortable for the full pipeline + obsd; the 4 PVCs need ~23 GB) | same node size |
| Rough cost | Hetzner CPX41 ≈ €0.04/hr (~€25/mo) · AWS t3.xlarge ≈ $0.16/hr · DO Droplet ≈ $0.09/hr | + a managed control-plane fee on some providers |
| Image load | `docker save … \| ssh … k3s ctr images import` (no registry) | needs a registry (ghcr/dockerhub) — extra step |
| obsd attach | run obsd **on the VM** vs local k3s kubeconfig; SSH-tunnel the console | run obsd from laptop over the cloud kubeconfig |
| Teardown | `k3s-uninstall.sh` then destroy the VM | delete the node pool / cluster |

**Per-provider gotcha (managed):** GKE/AKS/DOKS ship a default block-storage class, so PVCs
bind out of the box. **EKS does *not* ship a default StorageClass** — install the EBS-CSI
add-on and mark a `gp3` class default first, or the 4 StatefulSets stay `Pending` forever.

The rest of this plan assumes **k3s-on-a-VM** (cheapest) and calls out where the managed /
block-storage path differs; managed differs only at steps 1–3 and the storage class.

> **Teardown discipline:** the VM bills by the hour. **Destroy it when you're done** (or stop
> it). A `just cloud-down` recipe (see [§6](#6-new-artifacts-to-create)) should run
> `k3s-uninstall.sh` so you never leave a paid node running.

---

## 4. What changes vs local — the deltas (only three really matter)

| Concern | Local (kind) | Cloud (k3s on a VM) | Effort |
|---|---|---|---|
| **Create cluster** | `kind create cluster` (`just abb-up`) | `curl -sfL https://get.k3s.io \| sh -` on the VM | tiny |
| **Get the custom image in** ⚠️ | `kind load docker-image vigil-abb-sim:0.1` | `docker save` → `scp` → `sudo k3s ctr images import` (one image only) | tiny |
| **PVC storage class** | kind default `standard` (local-path) | k3s default `local-path` (same provisioner) — manifests bind unchanged. **For PVC-fill observability** swap in a block-storage class (managed default SC, or a CSI on the VM) | small *(only if you want the PVC-fill demo)* |
| **obsd attach** | `just obsd -- --kubeconfig …` from laptop | run obsd **on the VM** vs `/etc/rancher/k3s/k3s.yaml`; SSH-tunnel `:9095` (in-cluster Deployment is cleaner but **not built yet** — see §6) | small |
| **`pods/proxy` RBAC** ⚠️ | rides laptop admin creds (works) | the ClusterRole grants `nodes/proxy` but **not `pods/proxy`**; an **in-cluster** obsd with `--app-metrics-enabled` is **denied** the app-metrics scrape until you add it | tiny (one RBAC line) |
| **Console/API exposure** | `port-forward` | `ssh -L 9095:localhost:9095 user@vm` | tiny |
| **RBAC** | `just rbac` | identical `deploy/rbac/clusterrole.yaml` | none |
| **Node pin / taint** | abb-genix recipe handles it | identical recipe (k3s leaves the node schedulable) | none |
| **OOM events** | `--events-enabled` | identical (k3s surfaces the same events) | none |

The three that actually need a hand: **create the cluster**, **import the one image**, and
**point obsd at the cluster + tunnel the console.** That's the whole delta.

---

## 5. Step-by-step implementation

### Phase A — provision the cluster
1. **Create a VM** (4 vCPU / 8 GB / 80 GB), Ubuntu 22.04, on your provider. Note its
   `PUBLIC_IP`. Open SSH (22) only; we'll tunnel everything else.
2. **Install k3s** on the VM:
   ```bash
   ssh ubuntu@PUBLIC_IP
   curl -sfL https://get.k3s.io | sh -          # single-node cluster, default local-path storage
   sudo k3s kubectl get nodes                    # Ready in ~30s
   ```
3. **Apply the read-only RBAC** (same file as local):
   ```bash
   sudo k3s kubectl apply -f deploy/rbac/clusterrole.yaml   # copy the repo to the VM first (git clone / scp)
   ```

### Phase B — ship the one custom image
4. **Build the sim image** (on your laptop or on the VM) and **import it** into k3s
   (no registry needed):
   ```bash
   # on a machine with Docker + the repo:
   docker build -t vigil-abb-sim:0.1 deploy/workloads/abb-genix/sim
   docker save vigil-abb-sim:0.1 | ssh ubuntu@PUBLIC_IP 'sudo k3s ctr images import -'
   ```
   (All other images — influxdb, minio, postgres, mosquitto, grafana, curl — are public and
   pull automatically.)

### Phase C — deploy the workload
5. **Deploy the pipeline** (same recipe; it auto-labels the single node and applies the doc
   14 §3.3 customizations: gateway→unbounded, sensors→64Mi):
   ```bash
   sudo k3s kubectl ... # or: export KUBECONFIG=/etc/rancher/k3s/k3s.yaml; just abb-genix
   just abb-genix
   just abb-genix-gate        # MUST pass: pods Ready, 4 PVCs Bound, data flowing, contract intact
   ```
   If the gate passes, the interconnected pipeline + all 4 PVCs are live and flowing.

### Phase D — run Vigil against it
6. **Run obsd on the VM** (cleanest — no public API exposure). Build a Linux binary
   (`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./obsd/cmd/obsd`) and copy it over, or
   build on the VM. Then:
   ```bash
   ./obsd --kubeconfig /etc/rancher/k3s/k3s.yaml \
          --app-metrics-enabled --ksm-enabled --events-enabled \
          --forecast-roleseries --onset-enabled        # add forecasting/onset lanes for the demo
   # (console/API + health serve on :9095)
   ```
7. **View the console from your laptop** via an SSH tunnel:
   ```bash
   ssh -L 9095:localhost:9095 ubuntu@PUBLIC_IP
   # then open http://localhost:9095 on your laptop
   ```
   (Alternatively run obsd from your laptop with a kubeconfig whose server is the VM's public
   IP — but that needs k3s installed with `--tls-san PUBLIC_IP` and port 6443 exposed; the
   on-VM + tunnel path above avoids exposing the API server.)

### Phase E — drive the demo
8. **Inject failures** from the chaos corpus on the live cluster and watch Vigil react —
   the ordered scenario is [§5.1](#51-the-real-time-demo-timeline) below.

### 5.1 The real-time demo timeline

Run these in order; each maps to an exact file and an expected Vigil surface. Heal commands
are in each rig's header (and `corpus/labels/abb-genix-scenarios.md`).

| t | Action | File | What Vigil should show (and its provenance class) |
|---|---|---|---|
| 0–5m | **Baseline.** Just observe. | — | Full entity inventory + topology edges bound; SLO bars resolved for the 3 app metrics; gateway shows as **unbounded → resolvability-hole / Tier-B-ineligible**. Steady, **no phenomena**. (MEASURED) |
| 5m | **Load surge** | `corpus/chaos/abb-load-surge.yaml` | `PHEN_APP_LOAD_SURGE` on `opcua-gateway` — a rate crossing a declared bar. (MEASURED) Forecast may project the crossing just ahead, **with a band.** (PROJECTED) |
| 10m | heal, then **connectivity loss** | `corpus/chaos/abb-connectivity-loss.yaml` | `PHEN_APP_DATA_STALENESS` propagating along the chain to `asset-api`. (MEASURED) If `--cohypothesis-enabled`: a **direction-free co-onset** across the chain + an "author-the-direction" tab — **never a stated cause.** (MEASURED co-onset + AUTHORED tab) |
| 15m | heal, then **broker backpressure** | `corpus/chaos/abb-broker-backpressure.yaml` | `PHEN_APP_QUEUE_SATURATION` on `stream-processor`; CPU burn → `THROTTLING_CASCADE`. (MEASURED) |
| 20m | **PVC / disk fill** | `corpus/chaos/abb-historian-diskfill.yaml` (PVC) — **needs block storage**; on `local-path` use `corpus/chaos/disk-fill-ephemeral.yaml` (ephemeral, works everywhere) | `DISK_FILLING` on `genix-historian` (PVC, block-storage SC) **or** on a container (ephemeral-storage limit, any SC). (MEASURED) Forecast projects time-to-full **with a band.** (PROJECTED) |
| 25m | **Memory leak → OOM (flagship)** | `corpus/chaos/abb-analyzer-leak.yaml` | Working set climbs the 0.95×limit band → `MEMORY_LEAK`; forecast **warns** of the OOM crossing (PROJECTED, banded); then the kernel kills it → `OOM_KILL_CGROUP` (MEASURED). This is the money shot: **the forecast warned, the measurement confirmed — shown side by side, each labelled.** |

That last row is the whole thesis on screen: a PROJECTED early warning and the later MEASURED
fact **joined, never fused.**

---

## 6. New artifacts to create

The repo has **nothing cloud-specific yet** — these are the gaps to fill (small, additive).
Checklist:

- [ ] `deploy/cloud/README.md` — the VM + k3s bootstrap steps from [§5](#5-step-by-step-implementation) (provider-agnostic).
- [ ] `justfile` recipe **`cloud-up`** — documents/automates k3s install + RBAC apply (or just prints the commands; keep it macOS/Linux-identical per the cross-platform contract).
- [ ] `justfile` recipe **`abb-sim-ship`** — `docker save … | ssh … k3s ctr images import -` (the cloud equivalent of `abb-sim-build`'s `kind load`).
- [ ] `justfile` recipe **`cloud-down`** — `ssh … /usr/local/bin/k3s-uninstall.sh` (teardown discipline).
- [ ] *(if running obsd in-cluster)* add **`pods/proxy` (`get`) to `deploy/rbac/clusterrole.yaml`** — today it grants `nodes/proxy` only; the app-metrics lane scrapes pods via `pods/proxy`, so an in-cluster obsd with `--app-metrics-enabled` is **denied** without it (works from a laptop only because it rides admin creds).
- [ ] *(optional, "production-like")* an **obsd `Dockerfile` + `deploy/obsd-deployment.yaml`** (uses the existing `vigil-obsd` ServiceAccount, passes `--in-cluster`, a PVC/emptyDir for `--store-dir`/`--db`) so obsd runs **in-cluster** instead of from a binary. Today there's **no obsd image or Deployment** — the binary path in [§5](#5-step-by-step-implementation) avoids needing this for the demo.
- [ ] *(if you want the PVC-fill demo)* a **block-storage StorageClass** marked default (managed providers ship one; on k3s-on-VM install a CSI/Longhorn) so `kubelet_volume_stats` are emitted and `abb-historian-diskfill` → `DISK_FILLING` fires.
- [ ] *(optional)* a one-page **`deploy/cloud/demo-runbook.md`** = the [§5.1](#51-the-real-time-demo-timeline) timeline as a copy-paste script.

> Do **not** edit the abb-genix manifests, RBAC, or chaos rigs — they're already
> cloud-portable. The new files are all *additive bootstrap glue*.

---

## 7. Why cloud actually adds value (so the effort is justified)

- **Real block storage → real PVC-fill detection.** `abb-historian-diskfill` is gate-pending
  on `local-path` (kind *and* k3s emit no `kubelet_volume_stats`). On a cloud cluster with a
  **block-storage class** (managed default SC, or a CSI on the VM) the volume reports stats,
  so the `DISK_FILLING` phenomenon + its forecast finally exercise end-to-end — the one
  scenario you can't demo locally at all.
- **Real, isolated node.** Detection runs against a clean, dedicated kernel/cgroup tree, not a
  container-in-Docker — the OOM, throttle, and disk signals are closer to what a customer node
  produces.
- **A live, drivable demo.** You can sit in front of the console and inject faults one by one,
  showing MEASURED detection and PROJECTED forecasts appearing in real time on a real cloud
  cluster — the strongest possible "see how Vigil performs" story.

---

## 8. Scale testing vs. the demo — do you need 200–300 pods?

Short answer: **not for the demo; yes as a separate track.** "Scale" is two different axes:

| Axis | What it asks | Tested by | Pods needed |
|---|---|---|---|
| **Correctness / features** | does Vigil detect & forecast each failure *type* right? | ABB-Genix + `corpus/chaos/*` (this plan) | ~12–14 (shapes, not counts) |
| **Scale / performance** | does it stay correct & fast at hundreds of pods? | **nothing yet** — a real gap | 100s of cheap filler pods |

**There is currently no scale/perf test in the repo at all** (zero Go benchmarks, zero
load harness — verified). The design *assumes* scale (bound graph is in-memory, sized for
"thousands" of nodes, 1–2 hop walks, no graph DB — `techstack.md:83`) and has scale
*controls* (the Tier-B forecast budget is **capped per cycle** — `selection/tierb.go`,
doc 14 §5), but those have never been **exercised** at hundreds of pods.

**Keep these two exercises separate.** The cloud demo proves *features on real infra*; a
scale probe proves *it holds up at size*. Don't conflate them — three reasons:

1. **Different workload.** A scale probe uses **lightweight filler pods** (`pause`/nginx),
   not heavy realistic ones — you're stressing Vigil's *watch → bind → detect → scrape*
   loop, not the realism of each pod. Cheap to run.
2. **Single node caps out ~110 pods** (kubelet default `--max-pods=110`). To reach 200–300
   you either raise `--max-pods` on one **big** node (16+ vCPU) with filler pods, or use a
   **3–4 node pool** (no longer "single node"). A good cheap first probe: **~100 filler
   pods on one beefy node.**
3. **Different things to measure** (the scale SLOs):
   - detection **tick wall-time** stays under its interval as entities grow (linear, no cliff);
   - **scrape fan-out** keeps up — one cAdvisor/app-metrics request *per pod per tick* through
     the API-server proxy is the first real bottleneck;
   - obsd **memory** (informer caches for every pod/svc/PVC/endpointslice + the in-memory graph);
   - **QSS** (sqlite) write/read throughput per tick;
   - the **Tier-B budget holds** (forecasting stays scarce, doesn't fan out with pod count);
   - **★ honest degradation** — when a tick can't scrape everything, coverage stays truthful
     (marks stale/unobserved) instead of silently dropping entities. *This is the charter-critical
     one:* Vigil's whole promise is honest partial coverage, so the degradation path must be
     proven under real pressure — which only a scale test does.

**Recommended sequence:** (a) run the ABB-Genix demo first (this plan) — it's the deliverable
you asked for; (b) **then**, as a follow-up track, add a scale probe: a `Deployment` of N
`pause`/nginx replicas (start N≈100 on one node, grow toward 300 on a small pool), run obsd
with the lanes on, and watch the six SLOs above. New artifacts: a `deploy/scale/filler.yaml`
and a `just scale-up N` recipe; plus the *first* Go benchmarks for the detection cycle and the
bind step. None of this blocks the demo.

> Honest framing for stakeholders: "We've proven Vigil **detects the right things** (feature
> coverage). We have **not yet** proven it **scales to a large cluster** — that's a known gap
> with a planned probe, not a silent assumption."

---

## 9. Risks & honesty notes (don't over-claim)

- **Single node ≠ node-pressure realism.** One node can't show the 2-hop noisy-neighbour /
  node-pressure phenomena (that's what the 3-node `vigil` cluster, `deploy/kind/cluster.yaml`,
  is for — `deploy/CLAUDE.md` calls "two workers the floor" for those). On the cloud
  single-node, **demo container-/app-/cascade-level phenomena**, and say node-level pressure
  is out of scope for this cluster.
- **`local-path` has a PVC blind spot.** On k3s-on-VM default storage, `DISK_FILLING` on a
  **PVC** won't fire (no volume stats) — don't claim PVC-fill coverage there; either move to
  block storage or demo the **ephemeral-storage** disk rig instead, and say which.
- **The ABB product names are representative shapes, not real ABB software** (Mosquitto stands
  in for Edgenius, etc.) — the README already states this; keep stating it.
- **Cost / teardown.** The VM bills hourly; **always tear it down.** Budget a few dollars for a
  multi-hour demo.
- **Don't expose the API server publicly.** Prefer the on-VM obsd + SSH tunnel path; if you do
  expose 6443, lock it to your IP and use `--tls-san`.
- **Keep the discipline at the surface.** When demoing, **always label the class** — say
  "projected to cross ~X, band Y–Z" for forecasts and "is at / crossed at" for measurements.
  Never restate a forecast as a fact. (This is the charter; it's also what makes the demo
  credible.)

---

### Appendix — exact file references

- Single-node cluster: `deploy/kind/abb-cluster.yaml`
- Workload: `deploy/workloads/abb-genix/{00-namespace-config,10-backbone,20-sims}.yaml` + `sim/sim.py` + `sim/Dockerfile`
- Recipes: `justfile` → `abb-up`, `abb-sim-build`, `abb-genix`, `abb-genix-gate`, `abb-genix-down`, `rbac`, `obsd`
- RBAC: `deploy/rbac/clusterrole.yaml`
- Failure rigs: `corpus/chaos/abb-{load-surge,connectivity-loss,broker-backpressure,analyzer-leak,historian-diskfill,init-container-failure}.yaml`
- Ground truth: `corpus/labels/abb-genix-scenarios.md`
- Vigil contract drift check: built into `just abb-genix-gate`
