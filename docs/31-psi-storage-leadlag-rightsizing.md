# 31 — PSI, storage-IO, lead-lag timing, and a right-sizing advisory

> Status: PLAN — written before code, per the working agreement (`docs/22`).
> Branch `v6`. Companion docs: `docs/22` (which competitor parts we take and why),
> `docs/29` (CUSUM onset + direction-free cohypothesis), `docs/20` (assoc lane),
> `docs/07` (topological detection), `docs/09` (forecasting), `docs/12` (graph
> governance + release). The competitor under study is `ABB_Accelerator_Proto`
> (`mark-one`, code at `/tmp/abb_proto`); every claim about it below is grounded in
> their files, not their README.
>
> Discipline for this arc (restated): **build robust and complete; never fall back to
> a shallow proxy to make a goal look met.** Each adoption earns its place against real
> behaviour. A component that proves valueless after honest dev is **scrapped and
> recorded as scrapped**, not dressed up.

---

## 0. Validation status — EMPIRICAL (run before any implementation)

The feasibility claims below were **probed live, not inferred from docs** (the workflow's
research agent got the PSI verdict wrong — see §2.2). Reusable gate: `just psi-preflight`
(`deploy/preflight/psi-storage-preflight.sh`).

**Substrate matrix — what each cluster can actually emit, measured:**

| Signal | kind / LinuxKit (`vigil-abb`) | minikube / buildroot (`vigil-robust`, vfkit + dedicated disk) | What it takes to get it |
|---|---|---|---|
| **PSI** `container_pressure_*` (6 families) | ✅ **real, ZERO config** — 552 series at `/metrics/cadvisor`, no `KubeletPSI` gate | ❌ **kernel has `# CONFIG_PSI is not set`** — not fixable at runtime | a kernel with `CONFIG_PSI=y` + cgroup v2 (LinuxKit ✓, Ubuntu ✓, minikube buildroot ✗) |
| **container disk-IO** `container_fs_*` | ✅ real (already scraped) | ✅ real | cgroup v2 (universal) |
| **PVC fill** `kubelet_volume_stats_*` | ⚠️ **emits but NODE-FS-SCOPED** — every PVC reports the 485 GB node disk (directory-backed `csi-hostpath`) | ✅✅ **REAL per-PVC** — proven: a 5Gi PVC reports `capacity=5.20 GB, used=1.57 GB, fill=30.3%` on an LVM logical volume | a **block/LVM-backed CSI on a dedicated disk** (proven here with OpenEBS LVM on `/dev/vdc`) |

**The decisive finding:** no single cluster tried here emits all three with real values. kind
has PSI but directory-CSI can't isolate per-PVC fill; the minikube VM has real per-PVC fill
(LVM) but its buildroot kernel lacks PSI entirely. **The all-three substrate = a node whose
kernel has `CONFIG_PSI=y` (Ubuntu or LinuxKit) AND an LVM/block CSI on a dedicated disk.**
Both halves are now independently proven; the only remaining work is putting them on one node.

> **UPDATE (2026-06-22) — the all-three substrate now EXISTS and is live: AWS EC2 +
> Ubuntu 24.04 + k3s + AWS EBS CSI** (`docs/32`). One node (`m7i-flex.large`, kernel 6.17
> `CONFIG_PSI=y` + cgroup v2) emits, measured off the kubelet: **PSI 450 series / 6 families**
> (`/metrics/cadvisor`), **disk-IO 70 series** (`/metrics/cadvisor`), and **real per-PVC fill**
> — 4 EBS-backed PVCs reporting DISTINCT capacities (1/2/10/10 GiB), not the node disk.
> EBS CSI is simpler than the OpenEBS-LVM recipe below (dynamic per-PVC EBS volumes, no
> dedicated-disk `pvcreate`/`vgcreate`). obsd binds it from the Mac over the API; graph hash
> identical (cluster-agnostic confirmed). **So Steps 1–5 below are no longer substrate-blocked
> — the dev is the only thing left.** See the per-step status note appended to §7.

**Recipe that delivers all three (the recommended robust single-node cluster):**
1. A VM with an **Ubuntu** image (kernel `CONFIG_PSI=y`, default-enabled — like LinuxKit) — e.g. Lima/Colima on macOS, or any cloud single-node; **not** minikube's buildroot.
2. Attach a **dedicated disk**; `pvcreate /dev/<disk> && vgcreate vigilvg /dev/<disk>`.
3. Install **OpenEBS LVM LocalPV** (`lvm-operator.yaml`, v1.9.1) — note: the raw manifest
   needs `LVM_NAMESPACE` set on the node DS **and** controller, and on a single node either
   drop the controller `podAntiAffinity` or scale 0→1 so the env-patched pod schedules;
   for a single-node dev cluster set `CSIDriver.storageCapacity=false` to skip the scheduler
   capacity gate. StorageClass `provisioner: local.csi.openebs.io`, `volgroup: vigilvg`, default.
4. Leave the workload manifests `storageClassName`-free (they bind the default — the
   cluster-agnostic pattern; a customer's real CSI is their default).
5. Verify with `just psi-preflight` (PSI PASS) + a filling PVC (per-PVC `used/capacity`).

> Live state left running after this validation: kind `vigil-abb` (PSI/IO real; abb-genix
> migrated onto `csi-hostpath-sc`, dependency graph intact) and the minikube `vigil-robust`
> VM (OpenEBS LVM proven). Tear down the VM with `minikube delete -p vigil-robust` when done.

---

## 1. Goal, scope, and the guardrails every adoption must survive

The competitor's genuine telemetry edges are four things Vigil does not yet exploit:

1. **PSI** — `container_pressure_{cpu,memory,io}_{stalled,waiting}_seconds_total`, the
   kernel's pressure-stall accounting. A direct, low-noise saturation signal that today
   Vigil infers only indirectly (throttling counters, OOM events).
2. **PVC-fill + storage-IO observability** — `kubelet_volume_stats_*` (used/available/
   capacity per PVC) and `container_fs_{writes,reads}_bytes_total` / `io_time`. Vigil
   already scrapes the cAdvisor disk-IO counters (inert) but never the volume-stats.
3. **Lead-lag timing** ("who moved first") — a *measured* cross-correlation over a lag
   grid, to make the existing co-onset evidence sharper.
4. **A right-sizing advisory** — sustained-percentile vs requests/limits, surfaced as a
   recommendation an operator acts on (never auto-applied).

**Scope boundary.** This doc covers ingest + detection-bar authoring + the off-digest
lead-lag witness + the advisory lane. It does **not** propose any auto-remediation, any
learned threshold, or any agent write path. Those remain charter-forbidden.

### The guardrails (doc 01), and what each adoption must show

| Guardrail | What it forbids | How each adoption respects it |
|---|---|---|
| **Provenance = 3 immutable kinds** | inventing graph node kinds at runtime | PSI/PVC detection is authored as overlay threshold-rules + phenomena; no new kind. |
| **LLM agent has NO write tool** | agent authoring detection | PSI/PVC bars and lead-lag are authored by humans / produced by deterministic code; MCP stays read-only (27 tools, no write-back). |
| **In-digest must be replay-byte-identical** | sampled/learned inputs in the digest | PSI + volume-stats are monotonic counters → `rate()` is deterministic → **in-digest legitimate**. Lead-lag and right-sizing are NOT in-digest. |
| **Off-digest lanes are firewalled** | sampled lanes feeding the digest | lead-lag joins the existing firewalled cohypothesis candidate store; right-sizing is a read-only advisory. Neither feeds detection. |
| **New detection authored via governed overlay + versioned release** | hardcoded thresholds | every PSI/PVC bar ships in an overlay, graphlint-checked, cut as a new graph release with a named human. |
| **Cluster-AGNOSTIC** | service/namespace coupling | the metric families are agnostic; the *only* coupling is a k8s-**version**-conditional bring-up patch (PSI feature gate), which is orthogonal to the no-service/ns rule. |

The one honest tension is in the last row: PSI **bring-up** is k8s-version-specific
(§2). That is cluster-*version* conditioning, not service/namespace coupling — acceptable
under the charter, but it must be documented and gated, not silent.

---

## 2. PSI feasibility — VERDICT: config-only on this environment

**VERDICT: CONFIG-ONLY.** No custom node image, no kernel rebuild, no differently-built
cluster, no second disk to make the *metrics appear*. The competitor's BUILD_LOG claim
(that stock kind "lacked" PSI and needed bare-metal + a dedicated disk + host
`CONFIG_PSI` + the kubelet gate) is **partly stale for this machine**: the kernel
prerequisites are met out of the box on Docker Desktop's LinuxKit kernel, and a dedicated
disk only matters for generating realistic `io.pressure` *magnitude under load*, not for
availability.

### 2.1 What emits PSI, and the metric family

cAdvisor embedded in the kubelet, exposed on the kubelet's **`/metrics/cadvisor`**
endpoint — which `obsd` already scrapes (`FetchCAdvisor`, `obsd/internal/observe/scrape.go:135`).
The family is six counters:

```
container_pressure_cpu_stalled_seconds_total      container_pressure_cpu_waiting_seconds_total
container_pressure_memory_stalled_seconds_total   container_pressure_memory_waiting_seconds_total
container_pressure_io_stalled_seconds_total       container_pressure_io_waiting_seconds_total
```

`_stalled_` is PSI "full" (all tasks stalled — the harsher signal); `_waiting_` is PSI
"some". Both are emitted and valid. The competitor scrapes the `_stalled_` variant
(`/tmp/abb_proto/aggregator/queries.yaml`); we should ingest **both** and let the
authored bar pick. There is no separate cAdvisor version pin — the integration ships with
the kubelet, so the kubelet/k8s minor version is what governs availability.

### 2.2 The feature gate — what it does and does NOT gate (EMPIRICALLY CORRECTED)

> **CORRECTION (verified live, supersedes the research framing).** The design-workflow's
> research agent asserted `/metrics/cadvisor` returns **0** `container_pressure_` lines on
> v1.35 without the `KubeletPSI` gate. **A direct live probe of `vigil-abb` (k8s v1.35.0)
> refuted this: 552 `container_pressure_*` series are emitted with NO `featureGates` in the
> kubelet config and NO `--feature-gates` flag** (`grep` of `/var/lib/kubelet/config.yaml`
> and the kubelet cmdline both empty; `just psi-preflight` shows all 6 families). The agent
> deduced "no gate ⇒ no metrics" from the gate's lifecycle docs instead of probing — exactly
> the "validate before implementation" trap this section exists to avoid.

The reconciliation: **the `KubeletPSI` gate governs PSI-based *node conditions*, eviction,
and the *Summary API* PSI fields — none of which Vigil consumes.** The
`container_pressure_*` **metrics on `/metrics/cadvisor` are emitted by cAdvisor straight
from cgroup-v2 PSI and are NOT gated** by `KubeletPSI` on this `kindest/node` build. So the
metric path Vigil needs is **gate-free**.

| k8s minor | `KubeletPSI` (node-conditions/Summary-API) | cAdvisor `/metrics/cadvisor` PSI (what Vigil scrapes) |
|---|---|---|
| < v1.33 | gate absent — do NOT patch (fails kubelet) | emitted whenever the node is cgroup-v2 + `CONFIG_PSI` |
| v1.33–v1.35 | alpha/beta, off by default | **emitted regardless of the gate** (verified live on v1.35.0) |
| ≥ v1.36 | GA / locked-on | emitted |

**Conclusion:** for Vigil's metric path, **no feature gate and no patch are required** on a
cgroup-v2 + `CONFIG_PSI` node. Only add the gate if you separately want PSI node-conditions
(we don't). This removes the lone "version-conditional" wrinkle for the metric path — it is
cleanly config-free, not version-coupled.

### 2.3 Hard kernel prerequisites — all met live on this host

- kernel ≥ 4.20 — live node is **6.12.76-linuxkit** ✓
- `CONFIG_PSI=y` — `/proc/config.gz` shows `CONFIG_PSI=y` and `# CONFIG_PSI_DEFAULT_DISABLED is not set` (PSI on by default, **no `psi=1` boot param needed**) ✓
- cgroup v2 unified — Docker reports `Cgroup Version: 2` ✓
- per-cgroup PSI files live — `/sys/fs/cgroup/system.slice/containerd.service/cpu.pressure` shows real per-cgroup totals (this is what cAdvisor reads per container, not just the system-wide `/proc/pressure/*`) ✓

**kind / Docker-Desktop caveat (decisive).** kind nodes are containers sharing the Docker
host/VM kernel. PSI availability and cgroup version are dictated by the **LinuxKit VM
kernel**, not by the `kindest/node` image. "Does the kind image have cgroup v2?" is the
wrong question — the host VM does, and it has `CONFIG_PSI=y`, so the kind node inherits a
PSI-capable kernel for free on this setup.

### 2.4 The required env change

PSI is config-only, but kind **cannot add a feature gate to a running node** — the cluster
must be recreated (`just abb-down && just abb-up`). For the live single-node
`deploy/kind/abb-cluster.yaml` (k8s v1.35):

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: vigil-abb
nodes:
  - role: control-plane
    kubeadmConfigPatches:
      - |
        kind: InitConfiguration
        nodeRegistration:
          kubeletExtraArgs:
            feature-gates: "KubeletPSI=true"
```

For the 3-node `deploy/kind/cluster.yaml`, add the **same** `kubeadmConfigPatches` block
to the control-plane (`kind: InitConfiguration`) **AND to each worker** with
`kind: JoinConfiguration` (`kubeletExtraArgs.feature-gates: "KubeletPSI=true"`). Use
`kubeletExtraArgs`, **not** a `KubeletConfiguration.featureGates` map — kubeadm reads
`KubeletConfiguration` only from the first node and applies it cluster-wide, so a worker
would silently miss the gate.

> **NOTE — verified live, supersedes the optimistic framing above for `vigil-abb`.**
> The `just psi-preflight` script (`deploy/preflight/psi-storage-preflight.sh`) and the
> header of `deploy/kind/abb-cluster.yaml` record that on the current `kindest/node`
> kernel the kubelet's cAdvisor emits `container_pressure_*` **out of the box — no
> feature gate, no kubelet flag, no rebuild.** Treat §2.2's gate-patch as the *portable*
> answer (required on a vanilla v1.33–v1.35 cluster); on this exact image it is already
> on. Either way the action is config-only.

**Verify after bring-up:**

```
docker exec vigil-abb-control-plane curl -sk --cacert /etc/kubernetes/pki/ca.crt \
  https://localhost:10250/metrics/cadvisor | grep -c container_pressure_
```

On the live `vigil-abb` this returns **552 (un-patched)** — PSI is already on. It is `0`
only on a node whose kernel lacks `CONFIG_PSI` (e.g. minikube's buildroot — see §0), which
no gate can fix. So the gate-patch is a portable safety net for the *node-conditions* path,
not a requirement for the metrics Vigil scrapes.

### 2.5 The zero-value trap — a portability hazard to author around

k8s issue #136333: when `KubeletPSI` is on but the kernel lacks real PSI, the kubelet emits
the family as **static zero counters** rather than omitting them — silent garbage that a
naive `rate()` bar reads as "perfectly healthy, zero pressure." Not a risk on this host
(PSI is live), but a portability hazard. **Vigil must treat all-zero, never-moving
`container_pressure_*` as NO-DATA, not healthy** — authored as a silence-ledger /
blindspots floor (§4 row "anomaly", §8 risk R4-z). Also note the cardinality cost (k8s
#136642): 6 series × per-container expands the ingest:use ratio; the bar, not the ingest,
is the governance gate.

---

## 3. PVC + storage-IO observability

### 3.1 Cluster-agnostic mapping — PVC CEI + the `mounts` edge

PVCs are already first-class identity in Vigil (`obsd/internal/binding/binding.go`,
`bindPVC`; `obsd/internal/identity/normalize.go` `pvc()` mints `PVCUID`, time-aware on
`(ns, pvc-name, joinTime)`; v0.8.0+). Topology routes pods → PVCs via **`EdgeMounts`**
(`obsd/internal/identity/edges.go`). So:

- `kubelet_volume_stats_*` carry `{persistentvolumeclaim, namespace}` labels → the
  normalizer's PVC dialect resolves them to the **PVC CEI** with no new CEI kind and no
  new binding entity.
- `container_fs_*` carry the cAdvisor container labels → they resolve to the **container
  CEI** exactly like CPU/mem gauges today.

This is cluster-agnostic by construction: no service or namespace is hardcoded; the only
fixed anchor in the whole graph remains `kube-system` as the cluster-id.

### 3.2 The config-relative bar vs. the derived-ratio trap

The honest fill bar is **config-relative**: `used / capacity` (or `used` vs the PVC's
requested size) — a *declared* bar arriving from the spec, exactly the borrowed-normativity
pattern (`obsd/internal/forecast/eligibility.go` `ReasonDerivedRatio` documents the trap).

**The trap to avoid:** authoring the alarm on a *derived ratio whose denominator is itself
a moving series* (e.g. fill-rate relative to a rolling-mean of writes), or forecasting a
synthetic ratio stream. Vigil already refuses to forecast derived-ratio pseudo-streams
(`ReasonDerivedRatio`, and `ReasonNoStreamKey` for PVC pseudo-keys). The PVC fill bar must
therefore be:

- bar = `kubelet_volume_stats_used_bytes / kubelet_volume_stats_capacity_bytes` ≥ a
  **declared** fraction (authored in overlay, never learned), OR `used_bytes` vs the
  declared request; **and**
- eligibility-gated so a PVC with no capacity series (or all-zero, see §2.5) binds
  `StateOutOfScope` rather than fabricating a healthy reading
  (`obsd/internal/binding/compile.go` already maps unobtainable → `StateOutOfScope`).

The competitor's `_rightsize()` divides p95 by `requests/limits` (`/tmp/abb_proto/api/main.py`)
— that denominator *is* config (a fixed spec value), which is the safe direction; their
sin is elsewhere (single-shot p95, untested, namespace-hardcoded `factory-.*`), addressed
in §6.

### 3.3 The new kubelet `/metrics` Fetcher — and what does NOT need one

Two sub-cases, decided by which endpoint emits the metric:

- **`container_fs_*` (disk-IO)** is emitted on **`/metrics/cadvisor`**, which `obsd`
  already fetches. It arrives through the **existing** `FetchCAdvisor` → `ingestExposition`
  path (`scrape.go:271–378`), which accepts GAUGE and COUNTER families universally
  (`scalarType()` `scrape.go:514–525`). **No new Fetcher, no new Family, no new normalizer
  dialect** — disk-IO coverage is overlay-bar-only.
- **`kubelet_volume_stats_*` (PVC-fill) — CORRECTION (verified live on AWS 2026-06-22):**
  these do **NOT** ride `/metrics/cadvisor` (0 series there). They are emitted on the
  kubelet's **own `/metrics`** endpoint (4 series on the AWS box, one per bound PVC) — the
  endpoint `obsd` deliberately does **not** scrape today (`obtain.go` lists `kubelet` in
  `unscrapedEndpointTools`). The original framing here (and §4 "Ingest" row) wrongly assumed
  volume-stats were on `/metrics/cadvisor`; the live EBS cluster refutes it. **Therefore
  PVC-fill coverage DOES require the new kubelet `/metrics` Fetcher described below** — it is
  not plumbing-free. (disk-IO and PSI remain plumbing-free; only PVC-fill needs the Fetcher.)
- A **new kubelet `/metrics` Fetcher** is needed only if we later want kubelet-*own*
  metrics (the endpoint `obsd` deliberately does **not** scrape today — `obtain.go:72,88`
  lists `kubelet` in `unscrapedEndpointTools`). If a future signal lives there, add
  `FetchKubeletMetrics(ctx, f, nodes)` paralleling `FetchNodeExporter`, fetch path
  `"metrics"` (not `"metrics/cadvisor"`), and define a `FamilyKubelet` exporter family.
  **This is NOT required for PVC-fill** on the volume-stats path above.

> **Live caveat for `vigil-abb` (verified, supersedes optimism):** the default
> `rancher.io/local-path` provisioner makes hostPath-backed PVs the kubelet does not stat,
> so `kubelet_volume_stats_*` is **empty here** (0 series for 4 bound PVCs — a *provisioner*
> limit, not a kernel/node-image one). To exercise PVC-fill locally, install a
> metrics-capable CSI driver (`csi-driver-host-path` / OpenEBS / Longhorn) and re-run the
> preflight; on a customer's real CSI cluster it emits natively. Disk-IO (`container_fs_*`)
> IS available here.

### 3.4 What stays in-digest

Both families are honest exporter counters/gauges from the already-trusted cAdvisor source
→ **borrowed-normativity clean, in-digest legitimate**. `rate()` of a monotonic counter is
deterministic; volume-stats are point-in-time gauges canonicalized to UTC at ingest
(`scrape.go:272`) → replay-byte-identical. They belong **in-digest** alongside cAdvisor
CPU/mem, **not** in an off-digest lane. The lead-lag witness (§5) and the right-sizing
advisory (§6) are the only off-digest pieces of this doc.

---

## 4. Compatibility matrix

Verdict scale: **clean-additive** (no design needed) · **additive-with-care** (additive but
needs a guard) · **needs-design-work** · **structural-risk**.

| Layer | Verdict | Specific issue | Mitigation |
|---|---|---|---|
| **Ingest / scrape** | clean-additive | PSI + `container_fs_*` + volume-stats all arrive via existing `/metrics/cadvisor` → `ingestExposition` (`scrape.go:271–378`); `scalarType()` accepts COUNTER+GAUGE. | None for plumbing. Stream IDs (`CEI\|metric\|subID`, `scrape.go:336`) make each family a distinct, deterministic stream. |
| **RCA (root-cause chain)** | clean-additive | Chain stitches MEASURED-degraded workloads over conntrack **flow** edges; PVCs do NOT appear in flow edges (flow is L4 networking, `flow/graph.go`). Storage reaches the chain only via **authored** `mounts`/blast-radius, not the spine. | A PVC finding surfaces at its own PVC CEI as a distinct store row (`store/findings.go` PK = `entity_cei + phenomenon + graph_version`); no merge with pod/node findings. Author any PVC→pod relation as Node/Pod→PVC, never PVC→Node (no backward edge exists). |
| **Dependency-graph** | additive-with-care | PVC topology is `mounts` (pod→PVC) + `runs-on` (pod→node); there is **no backward PVC→Node edge**. A future PVC-anchored phenomenon needing node-IO context cannot walk backward. | Anchor storage-saturation at the **Node** (existing `STORAGE_SATURATION`, two-hop pod→PVC) or keep new bars pod-scoped. Enforced by the cascade checker's topological-relatedness rule (`detect/cascade.go:152–227`), caught at authoring time by graphlint. |
| **Topology** | clean-additive | Entity-kind hygiene: a "PSI above X" bar must bind to the right scope — container PSI on Container rules, node PSI on Node rules — or it scopes wrong. | graphlint overlay validation enforces entity-kind consistency between checks and the rule's anchor kind. |
| **Early-warning / forecast** | additive-with-care | PSI/`used_bytes` are forecastable monotonic-ish series; but PVC fill on churny pods and **derived ratios are NOT forecastable** (`ReasonDerivedRatio`, `ReasonNoStreamKey`). Per-pod PSI series break on HPA/rollout churn. | Forecast the **raw** `used_bytes` / PSI counter rate, never a synthetic ratio. Use churn-stable **role-series** keying (`forecast/roleseries.go`) so a workload's PSI survives pod churn. Each new forecastable class needs its own backtest gate. |
| **Anomaly** | additive-with-care | **Zero-value trap (§2.5):** all-zero never-moving PSI reads as "healthy." **CUSUM false-onset:** spiky `container_fs_writes` / `io_time` and PSI bursts can trip the EWMA-residual CUSUM (`onset/onset.go`) on a benign spike. | Author a **silence-ledger / blindspots floor**: "PSI present but all-zero over N hours ⇒ NO-DATA." Keep CUSUM's sustained-shift requirement (a single spike must not onset; doc 29 E8 = 3.3% false-onset on pure noise) and treat onset as off-digest evidence only. |
| **Assoc lane** | structural-risk | The assoc lane is **lag-0 Pearson, |r|≥0.6, capped at 256 streams O(n²)** (`assoc/assoc.go`). PSI (6/container) + fs + volume-stats can blow past 256 streams on a busy cluster, **silently truncating** which pairs are even considered. | Do NOT raise the cap blindly (O(n²) cliff). Restrict the assoc/cohypothesis candidate set to **bar-bound** or recently-onsetting streams before the cap binds; document that assoc coverage is a sampled subset above 256 streams (a known blind floor, surfaced in blindspots). |

**Explicit call-outs requested:**

- **assoc 256-stream cap.** With PSI multiplying stream count, the `O(n²)` 256-stream cap
  (`assoc.go`) will bind and silently drop pairs. The lead-lag work (§5) is safe *because*
  it runs **only on pairs that already survived** onset + `|r|≥0.85` + `PruneConfounders`
  (a handful, capped `coHypMaxOut=30`) — never a fresh full scan. But the *upstream* assoc
  cap is a real coverage limit that must be reported, not hidden.
- **CUSUM false-onset.** `onset.Detect` scans all gauge streams `O(n)`; PSI bursts and
  bursty disk-IO are exactly the spiky inputs that risk false onsets. Doc 29's live bake-off
  measured 3.3% false-onset on pure noise / 100% detect on real steps — acceptable only
  because onset is **off-digest evidence**, sustained-shift-gated, and never a digest input.
  Adding PSI must not relax the sustained-shift requirement.

---

## 5. Lead-lag timing — DECISION: KEEP and STRENGTHEN the cohypothesis surface

**Decision: option (a) — keep the direction-free `cohypothesis` surface; do NOT remove or
replace it.** It is the load-bearing charter artifact — the correct answer to exactly the
question the competitor answers *incorrectly*. What is genuinely thin is the **lead-lag
evidence** it carries: today the online co-onset producer surfaces only a lag-0 Pearson `r`
and `deltaSeconds` = the gap between two CUSUM onset times. That is real timing evidence but
coarse, detector-jitter-prone, and not cross-checked against the shape of the
cross-correlation.

### 5.1 Why NOT adopt the competitor's auto-arrow

`/tmp/abb_proto/correlation/engine/lagcorr.py` computes a lagged Pearson over
`{0,5,15,30,60,120}s` both directions and `best_directed = argmax|r|` → "src leads dst by
k". That **inference** is a charter violation and is not adoptable as-is:

- **no detrending** — two co-trending series produce spurious high `|r|` (their own agents
  reproduced `r=0.933` from two co-trending series, doc 22 E-battery).
- **no significance / multiple-comparison correction** — any of 6 lags × 2 directions can
  win by noise (~12 implicit comparisons, uncorrected).
- the production **36-sample window silently drops the 120s lag** — the lag grid is partly
  fiction.
- `argmax` over a near-symmetric cross-correlation **flips direction under tiny
  perturbations** (their Q1 attribution was RNG-order-fragile), and `argmax|r|` is exactly
  what **invents edges from common-cause confounders** (E4b): a shared driver makes both
  series peak at a small nonzero lag and the argmax reads it as causation.

So: **adopt the MEASUREMENT, refuse the arrow.** Operator burden in their design looks like
zero, but it is actually *higher* — the operator must disprove fabricated arrows. A clean
lead, surfaced as evidence, is cheaper to author from.

### 5.2 The rigorous online lead-lag witness (the spec)

A new pure producer (`leadlag`, or a method on the cohyp path) computes a lagged
cross-correlation **only for pairs that already survived** onset + `|r|≥0.85` +
`PruneConfounders` (`obsd/cmd/obsd/main.go:1969–1974`) — never a fresh `O(n²)` lag scan:

1. **Detrend.** First-difference both binned series (reuse `assoc.binSeries`, difference
   adjacent bins) so co-trend cannot manufacture `r`. Matches the offline harness's
   z-normalize + ParCorr intent.
2. **Lag grid.** A PINNED, DECLARED symmetric set in bins, e.g. `{-4,-2,-1,0,+1,+2,+4}` at
   the 15s assoc bin (mirror `harness/causal-discovery/causal.py` `LAG_SET`). Declared,
   never fit.
3. **Min effective-N.** Require overlap at the tested lag ≥ a declared floor (≥ assoc
   `MinOverlap=8`); reject any lag whose shifted overlap drops below it. This **structurally
   forbids** the competitor's silent-120s-lag bug.
4. **Significance.** A **seeded** circular/block-permutation test: shift one differenced
   series K times (declared K, e.g. 200), recompute peak-lag `|r|`, report the empirical
   `p` = fraction of shuffles exceeding observed. Surface the witness only if `p < α`
   (declared, e.g. 0.05) AND `|r_lag|` clears a declared floor. The permutation null
   accounts for searching the grid (each shuffle searches the same grid) — this is the
   multiple-comparison defense.
5. **Peak lag.** The bin offset of `max|r_detrended|` on the surviving grid → reported as
   `deltaLagSeconds` with a **consistency flag** vs the onset-time delta (do the two
   independent witnesses AGREE on order?).

Output is a MEASURED row:
`{lagPeakSeconds, lagPeakRDetrended, lagP, lagConsistentWithOnset, effectiveN}`. **No
`argmax`→direction.** The peak lag is sign-carrying EVIDENCE, surfaced exactly like
`observedFirst` already is.

### 5.3 Determinism, firewall, and payload hygiene

- **Determinism:** pure function (same samples+params ⇒ byte-identical), like `onset.Detect`
  / `assoc.Associate`. Permutation shuffles MUST be seeded by a **content-derived** seed
  (hash of the sorted pair key + window bounds), never time/global RNG — this is precisely
  the fix for the competitor's RNG-order-fragility.
- **Firewall:** output is a firewalled `candidate.KindCausalHypothesis` row; `firewall_test.go`'s
  guarantee that the deterministic path never reads candidates is untouched. It reads only
  the already-snapshotted hot series the cohyp loop reads — no new digest input.
- **Content-id hygiene:** `lagPeakSeconds` / `lagP` MUST be payload-only, **excluded from
  the content id** (the id stays = the pair, `cohypothesis.go:102–104`), or per-cycle float
  jitter floods the firewalled store with near-duplicate candidates.
- **Distinct field names:** `levelCoefficient` (the existing lag-0 level `r`) vs
  `lagPeakRDetrended` (the new differenced-series `r`) — different quantities, never
  conflated.
- **Honest silence:** a flat/too-short differenced series yields a meaningless peak lag —
  emit **no** witness, never a `0s` "contemporaneous" default (else every quiet pair falsely
  corroborates).

### 5.4 MCP consumption — present TWO witnesses, never a direction

In `obsd/internal/mcp/server.go`, `toolCausalHypotheses` (`server.go:377–379`) instructs the
agent to present **both** witnesses (onset order + detrended peak lag) and the consistency
flag as a **strength-of-lead** signal: *"two independent measurements agree these co-moved
with b appearing to follow a by ~N s; this is a lead to investigate, NOT a direction."* When
the two witnesses DISAGREE or `lagP` is not significant, downgrade to *"co-moved, order
unclear."* `validate_claim`'s generated-causation flag must still fire on any stated arrow, so
a lead-lag can never be laundered into a cause in `emit_advisory`.

### 5.5 The offline lane stays

Keep `discovery.go` / `harness/causal-discovery/causal.py` unchanged — the deeper PCMCI
confounder-pruned shortlist (still DIRECTION-FREE). The online lag witness is the
lightweight always-on first pass; the offline harness is the rigorous second pass. **Author
nothing new in the base KG** — the whole feature is off-digest/candidate-only; the existing
`CausalDirectionRequest → OverlayYAML` path (`api/causalhypothesis.go`) is the only way a
direction is ever authored, by an operator. Wire behind the existing `--cohypothesis-enabled`
flag; off by default ⇒ byte-identical replay preserved.

---

## 6. Right-sizing advisory

### 6.1 Lane shape — off-digest advisory + read-only MCP tool

A new **off-digest advisory lane** (pattern of the alerting lane, `docs/30`): it reads the
same scraped series the digest reads, computes a recommendation, and **publishes it as a
CLASSED fact** — it never gates detection, never writes to the cluster, and with it off the
deterministic path and replay are byte-identical. Surfaced via a **read-only MCP tool**
(`get_rightsizing_advice` or similar) so the agent can relay it; the agent has no write path
and the tool returns only the computed advisory, never an action.

### 6.2 The rigorous derivation (fix every flaw in `_rightsize()`)

The competitor's `_rightsize()` (`/tmp/abb_proto/api/main.py`) is deterministic and its
*direction* is sound (compare a percentile to the **config** request/limit, headroom target
`p95×1.3`), but it is single-shot, untested, and namespace-hardcoded `factory-.*`. The Vigil
version must be:

1. **Sustained percentile, not single-shot.** A high percentile (p95/p99) over a declared
   window (e.g. `[24h]`), required to be **sustained** — not one spike. Reclaim if
   `p95 < f_low × request` (declared `f_low`, e.g. 0.5); resize-up if `p95 > f_high × limit`
   (declared `f_high`, e.g. 0.85); headroom target `p95 × h` (declared `h`, e.g. 1.3).
2. **Stability gate.** Only advise when the series is **stable** over the window
   (low coefficient-of-variation / no active onset / not mid-rollout). A churny or
   ramping workload yields **no advice** (honest silence), not a noisy recommendation. This
   is the discipline the competitor lacks.
3. **QoS awareness.** Respect the pod's QoS class: a **Guaranteed** pod (requests==limits)
   must not be advised into Burstable by a reclaim, and a **BestEffort** pod (no
   requests/limits) has no config bar to compare against → **out of scope**, not a fabricated
   number. The advice carries the QoS class it assumes.
4. **Storage right-sizing.** Same shape over `kubelet_volume_stats_used_bytes` vs the PVC's
   **requested** size: advise a larger PVC when sustained-used approaches request, or flag an
   over-provisioned PVC when sustained-used is a small fraction of request. Gated by the same
   PVC eligibility as §3.2 (no capacity series ⇒ out of scope).
5. **Cluster-agnostic.** No `factory-.*` regex — the lane operates over whatever CEIs the
   binding produced; namespace/service never hardcoded.

### 6.3 Charter framing

Right-sizing is **advisory, not detection**: it produces a recommendation, not a phenomenon,
so it authors nothing in the KG and needs no overlay/graph release. It is deterministic
(percentile-over-window is a pure function of the samples), but it is **off-digest** because
it is not one of the closed set of in-digest primitives and must never enter the replay
fingerprint. It is a CLASSED fact (grounded in measured percentiles + declared config), and
`validate_claim` labels it as a recommendation, never an executed action.

---

## 7. Ordered build plan with validation GATES

Each step is independent enough to land + verify before the next. Gates state what *proves*
the step, not just that code compiles.

**Step 1 — PSI env + scrape.** Recreate `vigil-abb` with the `KubeletPSI` patch (§2.4);
confirm `obsd` ingests the six `container_pressure_*` families.
- **GATE (`just psi-preflight`):** `docker exec ... grep -c container_pressure_` `> 0`, AND
  obsd shows the families as streams keyed to container CEIs, AND the all-zero case is
  flagged NO-DATA not healthy. (The preflight already PASSES PSI + disk-IO on this host;
  PVC-fill is an advisory WARN until a metrics-capable CSI is installed.)

**Step 2 — volume-stats / disk-IO ingest + the PVC fill bar.** Confirm `container_fs_*` and
`kubelet_volume_stats_*` (on a CSI-capable cluster) ingest via the existing `/metrics/cadvisor`
path; author the config-relative PVC fill bar in an overlay; cut a versioned graph release.
- **GATE:** graphlint passes on the new overlay (entity-kind consistent, §4 topology row);
  replay digest **byte-identical** with the bar present but un-crossed; a synthetic
  filled-PVC fixture fires `VOLUME_*`/fill at the PVC CEI as a **distinct store row**
  (`store/findings.go` PK), never merged with a node/pod finding. PVC bar binds
  `StateOutOfScope` (not "healthy") when no capacity series exists.

**Step 3 — compatibility hardening.** Author the PSI detection bar(s) (overlay + release);
add the silence-ledger / blindspots zero-value floor; verify the second-order cascade tests
still hold with PSI added as a required member.
- **GATE:** `detect/secondorder_test.go` (`TestStorageSaturationTwoHop`,
  `TestBrokenFirstHopNeverFabricates`) pass with PSI added — completeness drops (more
  required members) but quality stays **degraded, not full**, and a broken first hop still
  **never fabricates**. Assoc stream count above 256 is reported as a sampled subset, not
  silently dropped. Zero-pressure nodes light up **no** bar.

**Step 4 — lead-lag witness.** Implement the `leadlag` pure producer (§5.2) downstream of
`PruneConfounders`; extend the cohyp payload + `api/causalhypothesis.go`; tighten the MCP
framing.
- **GATE:** a sweep test mirroring `onset/sweep_test.go` + the competitor E8 battery:
  byte-identical output across seeds for the same content; the E4b common-cause confounder
  produces **no significant lead** (`lagP ≥ α`); a genuine ≥5 s lead is detected with the
  correct sign; replay byte-identical with `--cohypothesis-enabled` off; `firewall_test.go`
  still green; `lagPeakSeconds` excluded from the content id (no duplicate-candidate flood).

**Step 5 — right-sizing advisory.** Build the off-digest advisory lane + read-only MCP tool
(§6); wire behind its own flag, off by default.
- **GATE:** deterministic output for a fixed sample window; a churny/ramping fixture yields
  **no advice** (stability gate); a Guaranteed pod is never advised into Burstable; a
  BestEffort pod is **out of scope**; replay byte-identical with the lane off;
  `validate_claim` labels the output a recommendation, never an action.

### 7.1 Per-step status against the live AWS cluster (2026-06-22)

The substrate blocker is gone (§0 UPDATE); each step's remaining work, re-scoped:

| Step | Emission on AWS | Remaining dev | Size |
|---|---|---|---|
| **1 — PSI scrape** | ✅ 450 series ingesting via existing `FetchCAdvisor` (no patch, no gate) | none for ingest; PSI is scraped-but-**inert** (visible as raw streams, no bar) | done (ingest) |
| **2a — disk-IO bar** | ✅ `container_fs_*` (70 series) already ingested | author one overlay bar against `container_fs_*` + graph release | **small** |
| **2b — PVC-fill bar** | ✅ REAL per-PVC (4 EBS vols, distinct caps) — but on the kubelet `/metrics` endpoint | **NEW kubelet `/metrics` Fetcher** (`FetchKubeletMetrics`, §3.3 correction) + PVC→pod join + config-relative fill bar + release | **medium** |
| **3 — PSI detection bar** | ✅ emitting | author PSI overlay bar(s) (container + node scope) + zero-value NO-DATA floor + release; cascade tests hold | **small–medium** |
| **4 — lead-lag witness** | n/a (off-digest, runs on assoc survivors) | `leadlag` pure producer downstream of `PruneConfounders` + cohyp payload + seeded permutation test | **medium** |
| **5 — right-sizing advisory** | ✅ all input series present (cpu/mem percentiles + PVC used vs request) | off-digest advisory lane + read-only MCP tool, behind its own flag | **medium** |

**Recommended order on AWS:** Step 3 (PSI bar — the competitor's signature signal, cheapest
high-value win, already scraped) → Step 2a (disk-IO bar) → Step 2b (PVC-fill Fetcher+bar, the
storage payoff the AWS box was built for) → Step 4 (lead-lag) → Step 5 (right-sizing). Each is
a governed overlay + versioned release with a graphlint + replay-byte-identical gate.

---

## 8. Risk register

| # | Risk | Likelihood / Impact | Mitigation |
|---|---|---|---|
| **R1** | **kind PSI realism** — kind shares the host kernel and a single disk, so `io.pressure` magnitude under load is small/unrealistic; PSI *availability* is real but its *dynamics* are muted vs bare metal. | High / Med | Treat kind as availability + correctness proof, not magnitude calibration. Calibrate PSI bars on a real CSI/bare-metal cluster; on kind, validate the wiring and the zero-value floor, not the threshold value. Document that a dedicated disk only buys magnitude. |
| **R2** | **assoc 256-stream cap** — PSI multiplies stream count; the `O(n²)` cap (`assoc/assoc.go`) binds and **silently truncates** considered pairs. | High / Med | Don't raise the cap (cliff). Restrict assoc/cohyp candidates to bar-bound or recently-onsetting streams before the cap; report assoc coverage as a sampled subset above 256 in blindspots. Lead-lag runs only on already-pruned survivors, so it is unaffected. |
| **R3** | **monotonic PVC band** — PVC `used_bytes` is near-monotonic; a forecast band on it can creep or a derived fill-ratio is unforecastable. | Med / Med | Forecast the **raw** `used_bytes`, never a derived ratio (`ReasonDerivedRatio`); use role-series keying for churn; each forecastable class gets its own backtest gate. Reset/refill events (PVC resize) handled by the existing regime-shift flag. |
| **R4** | **CUSUM spikiness** — bursty `container_fs_writes` / PSI spikes trip the EWMA-residual CUSUM on benign bursts. | Med / Low | Keep the sustained-shift requirement (doc 29 E8: 3.3% false-onset on noise). Onset stays off-digest evidence, never a digest input; do not relax sustained-shift to chase PSI bursts. |
| **R4-z** | **PSI zero-value trap** (k8s #136333) — all-zero never-moving PSI reads as "healthy" on a PSI-less cluster. | Med / High (portability) | Author a silence-ledger / blindspots floor: "PSI present but all-zero over N hours ⇒ NO-DATA." Eligibility-gate any PSI bar to require ≥1 non-zero sample in the window. |
| **R5** | **operator burden** — lead-lag and right-sizing add surfaces the operator must read and judge. | Med / Low | Both are evidence/recommendation surfaces, NOT alarms — they don't page. Lead-lag downgrades to "order unclear" when witnesses disagree; right-sizing stays silent on unstable workloads. The competitor's auto-arrow / single-shot resize would be *higher* burden (disproving fabrications). |
| **R6** | **version-conditional bring-up** — the `KubeletPSI` patch differs by k8s minor (fails < v1.33, no-op ≥ v1.36). | Low / Low | Document the version table (§2.2); the preflight reports the gate state. This is cluster-*version* conditioning, orthogonal to no-service/ns coupling. |

---

## 9. What we ADOPT / IMPROVE / DISCARD, and the cluster-agnostic + scale verdict

### ADOPT (as-is, charter-clean)
- **PSI scraping** — `container_pressure_*` from the cAdvisor endpoint we already hit. A
  direct saturation signal, in-digest legitimate, config-only to enable.
- **PVC-fill + disk-IO observability** — `kubelet_volume_stats_*` + `container_fs_*` via the
  existing scrape path, resolved to PVC/container CEIs with no plumbing change.
- **The lagged cross-correlation MEASUREMENT** — the *core* of their `lagcorr.py`, stripped
  of the argmax→arrow step.
- **The right-sizing DIRECTION** — percentile-vs-config (request/limit) comparison; their one
  sound idea.

### IMPROVE (adopt the idea, fix the method)
- **Lead-lag** — add detrending (first-difference), a **declared** symmetric lag grid, a
  **min effective-N** floor (kills their silent-120s-lag bug), a **seeded permutation**
  significance test (kills their RNG-fragility + multiple-comparison hole), and a
  **consistency flag** against the onset-time witness. Surfaced direction-FREE.
- **Right-sizing** — sustained percentile (not single-shot), a **stability gate** (silent on
  churny/ramping workloads), **QoS awareness** (Guaranteed/BestEffort handled honestly),
  storage right-sizing, and **no namespace regex**.

### DISCARD (charter-incompatible or worthless)
- **`best_directed` auto-arrow** (`lagcorr.py`) — invents direction from confounders (E4b)
  and zero-lag (E3b); the human authors the arrow.
- **The hardcoded `STORAGE` env coupling + "same-node-couples-everything"** — their
  `ebpf_edges=set()` is display-only; Vigil's flow edges are MEASURED conntrack, not a
  hardcoded quartet.
- **Namespace-hardcoded `factory-.*`** in `_rightsize()` — violates cluster-agnostic.
- **Single-shot p95** with no stability/QoS gate — replaced by the rigorous derivation (§6.2).

### Cluster-agnostic verdict
**PASS, with one documented exception.** The metric families and every bar are
service/namespace-agnostic; identity resolves through the existing CEI/edge model with no
new coupling; `kube-system` remains the sole cluster-id anchor. The single exception is the
PSI **bring-up patch**, which is k8s-**version**-conditional — orthogonal to the
no-service/namespace rule and surfaced by the preflight, not hidden.

### Scale verdict
The honest scaling pressure is **cardinality + the assoc cap**: PSI adds ~6–12 series per
container and volume-stats ~3 per PVC, expanding the ~30:1 ingest:use ratio and binding the
`O(n²)` 256-stream assoc cap on busy clusters. Ingest itself scales (universal parser, no
per-metric code); detection scales (bar-bound streams only). The lead-lag work is bounded by
construction (runs on the already-pruned `coHypMaxOut=30` survivors). The watch item is
**assoc coverage becoming a sampled subset above 256 streams** — acceptable only because it
is *reported as a blind floor*, never silently dropped. Net: additive at this cluster's
scale; the assoc cap is the first thing to revisit as stream count grows.
