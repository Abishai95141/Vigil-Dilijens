# Cloud test plan — Vigil on a single-node AWS cluster

> **The goal:** stand up *one* cloud VM that behaves like a real industrial-edge node,
> run the ABB-shaped digital twin on it at production-ish scale, then break it the way a
> plant actually breaks — and watch Vigil report the truth and *only* the truth. This is
> the encore to the laptop k3s proof: the laptop already showed the engine is correct;
> the cloud node adds the two things a laptop can't give us — **hundreds of pods** and
> **real node-pressure (PSI)** — and proves the determinism dividend (capture on the
> cloud, replay byte-identical at home).
>
> Exact copy-paste commands live in [cloud-runbook-aws-azure.md](cloud-runbook-aws-azure.md).
> *This* doc is the **plan and the reasoning** — what to take, how to set it up, and the
> phased test that mirrors a real industrial system.

---

## 1. Why a single big node is the *correct* industrial model

ABB Theme-2 names *"single-node clusters commonly used in edge and industrial
environments."* That is not a simplification — it's the reality of OT/edge:

- A factory-floor cell, a substation, a ship, a wind turbine controller runs **one rugged
  box**, not a multi-AZ managed cluster. k3s/MicroK8s on a single node is the norm.
- The interesting failures are **local resource contention**: a historian saturating the
  one disk, a broker leaking into an OOM, a burst throttling the inference pod — all
  competing for *one* node's CPU/memory/disk/PSI. That is exactly what a single packed VM
  reproduces, and exactly where Vigil's "join, never fuse" restraint is tested.

So we deliberately do **not** use EKS/managed multi-node. One generously-sized VM packed
with pods is closer to the target, far cheaper, and keeps the node-exporter/cAdvisor/PSI
lane intact (managed serverless kills node access).

---

## 2. The instance — what to take on AWS, and why

**Recommended: `m6i.2xlarge` — 8 vCPU / 32 GiB / EBS gp3 80 GiB, Ubuntu 24.04 LTS.**

Why this one:

| Need | Why m6i.2xlarge fits |
|---|---|
| **Hundreds of pods** | k3s `--max-pods=300`; ~300 busybox/sim pods need ~6–8 GiB for shims + the workload. 32 GiB leaves headroom for obsd + the OS. |
| **Real PSI under load** | the ABB chaos (fsync floods, leaks, bursts) must actually stress *one* node's disk/mem so `/proc/pressure/*` crosses bars. 8 real vCPU + a single gp3 volume gives genuine, observable contention. |
| **Deterministic CPU** | `m6i` = fixed-performance Intel Ice Lake (no burst credits). Burstable `t3/t4g` throttle unpredictably and would corrupt the timing/forecast story — **avoid them.** |
| **gp3 EBS** | a single network-block volume mirrors industrial NAS/iSCSI storage far better than local NVMe; its bounded IOPS/throughput make the historian's PVC-I/O→restart coupling realistic (the laptop's fast NVMe was *too* fast to stall reads — see [2026-06-brutal-test-campaign.md](2026-06-brutal-test-campaign.md) §2.5). |
| **Cost** | ~**$0.384/hr** on-demand (ap-south-1). A full campaign (a few hours + an overnight soak) is a few dollars. |

**Sizing alternatives:**

| If you want… | Take | Notes |
|---|---|---|
| The standard run (recommended) | `m6i.2xlarge` (8/32) | `--max-pods=300`, the whole plan below. |
| Push to ~500–800 pods | `m6i.4xlarge` (16/64) | raise `--max-pods=500`; widen the pod CIDR (`--cluster-cidr=10.42.0.0/16`) since the default /24 caps at ~254 IPs. |
| Cheapest "does it run at all" | `m6i.xlarge` (4/16) | `--max-pods=150`; fine for restraint + the chaos loop, tight for the big scale tier. |
| Cross-node cascade realism | 2–3× `m6i.xlarge` via EKS Standard (node-based, **not** Fargate) | costs more, less theme-aligned; only if you specifically need cross-node edges. |

**Region & disk:** any region with `m6i` (e.g. `ap-south-1` Mumbai for low latency from
India). gp3 80 GiB is plenty; the historian's 3 GiB TSDB + pod images + capture bundles
fit comfortably. Use the gp3 *default* 3000 IOPS / 125 MB/s — the bounded throughput is a
feature here (it makes disk saturation observable).

---

## 3. Setup — first, stand the rig up

**Prerequisites (yours):** an AWS account with billing, the `aws` CLI authenticated
(`aws configure`), and an SSH key. That's all — you create the VM, I drive the rest.

**Step 1 — launch the VM** (SSH-only security group; obsd's :9095 never public). Exact
commands: [cloud-runbook-aws-azure.md](cloud-runbook-aws-azure.md) §A.1–2. In short:
`m6i.2xlarge`, Ubuntu 24.04 AMI, gp3 80 GiB, SG allowing **only** TCP 22 from your IP.

**Step 2 — one-command bootstrap.** SSH in, then:
```bash
sudo apt-get update && sudo apt-get install -y git
git clone https://github.com/Abishai95141/Vigil-Dilijens && cd Vigil-Dilijens
MAX_PODS=300 ./deploy/cloud/bootstrap-vigil-edge.sh
```
That installs k3s (`--max-pods=300`), the toolchain, builds obsd CGO-free, and deploys
the rig: **node-exporter** (the bit the laptop lacked — makes node-PSI observable),
**kube-state-metrics** (the OOM/restart object-state lane), and the **ABB industrial-edge
digital twin**. After it returns, the cluster *is* a small industrial plant.

---

## 4. The industrial system we mirror (what's inside the cluster)

The digital twin (`deploy/workloads/industrial-edge/`) is a deliberately ABB/OT-shaped
plant, not an e-commerce app:

| Component | Real-world analogue | Role in the test |
|---|---|---|
| `mqtt-broker` | the plant message bus (sensors → everything) | leaks → OOM under the leak chaos |
| `historian` (PVC) | the process historian / time-series DB on bulk storage | saturates the disk; serves SCADA queries |
| `sim-current/power/temperature` | field device telemetry publishers (bounded) | steady load + declared limits (real bars) |
| `sim-vibration` (**unbounded**) | a device with no declared SLO | tests the *honest null* — Vigil says "unbounded, no bar" instead of inventing one |
| `edge-inference` | the on-edge ML/analytics pod | throttles under burst (CPU limit) |
| `scada-dashboard` | the HMI / SCADA reader | restarts when its historian query stalls |

These are the entities ABB's questions are *about*: "how do PVC I/O patterns link to pod
restarts," "what happens when the broker leaks," "how does a telemetry burst affect
inference." Each maps to a chaos scenario in `corpus/chaos/industrial/`.

---

## 5. The test plan — brutally testing Vigil, the way a plant breaks

Run in phases. Each phase has a **pass criterion** that is a *charter property*, not just
"a finding appeared." The point is what Vigil refuses to assert as much as what it catches.

### Phase A — Baseline restraint (the hardest bar to clear)
Bring obsd up against the healthy twin. **Pass:** every entity discovered, **zero false
findings**, and `sim-vibration` surfaced as *"unbounded — no early-warning eligibility"*
(the honest null). A tool that invents problems at baseline is noise; Vigil must be quiet.
```bash
export VIGIL_TEST_KUBECONFIG=$HOME/k3s.yaml
just e2e        # detection + restraint + live replay determinism + auth, in one suite
```

### Phase B — Scale (the "hundreds of pods on one node" claim)
```bash
VIGIL_SCALE_N=300 just scale
```
**Pass:** obsd discovers ~all 300, **RSS stays bounded** (<1 GiB; we measure KiB/entity),
**0 mis-joins at scale** (the silent killer — two different pods must never merge into one
identity), and after churn-to-zero the identity layer **GCs back to baseline, still 0
mis-joins**. This is the live scrape→identity→bind path under real load; the hermetic
`just bench` separately proves detection cost to 5,000 entities.

### Phase C — The ABB chaos loop (inject real faults, read Vigil's verdict)
Apply each scenario against the live twin with obsd watching
(`bin/obsd --ksm-enabled --app-metrics-enabled …`). For each, the **pass** is a pair: the
right MEASURED finding(s) appear, **and** the forbidden fabrication does *not*.

| Scenario | Real fault injected | Vigil must report | Vigil must NOT do |
|---|---|---|---|
| `abb-mqtt-leak-oom-reconnect-cpu` | broker memory leak → real OOM (exit 137) | `PHEN_MEMORY_LEAK` vs the **declared** 96 Mi bar (×0.95); `OOM_KILL_CGROUP` via KSM | invent a broker-OOM → pipeline-CPU **causal cascade** (co-occurrence only) |
| `abb-historian-io-scada-restart` | historian fsync flood saturates the **gp3** disk; SCADA query stalls → restart | `STORAGE_SATURATION` (node PSI, now observable via node-exporter) + the SCADA restart | mint a `STORAGE_SATURATION → PROBE_FAILURE_RESTART` edge (no authored edge ⇒ no cascade) |
| `abb-telemetry-burst-inference-throttle` | telemetry burst → inference hits CPU limit | `THROTTLING_CASCADE` on the inference pod (precise, since PSI is real) | relate the burst to the throttle as *cause* |
| `abb-unbounded-workload` | the unbounded vibration sim grows freely | honest *"unbounded, Tier-B-ineligible"* | invent a bar for a workload with no declared limit |
| `abb-historian-disk-forecast` | slow PVC fill toward a bar | a PROJECTED forecast **with an uncertainty band** | state the forecast as MEASURED, or collapse the band to a line |

> **gp3 is what makes Phase C land where the laptop couldn't.** On the laptop's fast NVMe
> the historian read returned in 0.28 s — too fast to stall — so we sized a 3 GiB cold
> scan to force it. On a bounded gp3 volume the disk genuinely saturates, so the coupling
> is natural and `STORAGE_SATURATION` fires from real PSI. Same scenario, more realistic.

### Phase D — Soak (drift over time)
```bash
VIGIL_SOAK_DURATION=4h just soak     # or overnight
```
**Pass:** RSS rises as the rings fill then **plateaus** (no slow leak), **0 mis-joins**
throughout. The laptop already showed this over 30 min (RSS 96→137 MiB, plateau); the
cloud run extends it across hours on a larger entity set.

### Phase E — The determinism dividend (the strong demo)
Capture a window on the cloud node, pull the bundle home, replay it on your laptop:
**same digests, byte-for-byte.** This is the replay guarantee proven *across machines* —
the deepest evidence that nothing in the path depends on wall-clock, host, or scale.
```bash
# on the VM:  bin/obsd --db /tmp/v.db --store-dir /tmp/cap --ksm-enabled & sleep 300; kill -INT %1
# at home:    scp -i vigil-edge.pem -r ubuntu@$IP:/tmp/cap ./cloud-cap && bin/replay -bundle ./cloud-cap
```

### Phase F — Forecast gate (optional, heavy)
Capture a *slow* creep (minutes, not the ~1-min fast leaks) and run the real-model gate
(`just forecast-gate /tmp/fcap timesfm`) to prove the narrow forecasting clock against a
bar with a real uncertainty band — the one place PROJECTED data is produced, kept strictly
non-gating (detection never waits on it).

---

## 6. Pass criteria in one line each

1. **Quiet at baseline** (no invented findings; honest nulls stated).
2. **Hundreds of pods, 0 mis-joins, bounded RAM, GC after churn.**
3. **Right MEASURED finding per fault, every unobservable member named.**
4. **No fabricated cause** — co-occurrence stays co-occurrence; no learned/invented bars.
5. **PROJECTED never restated as MEASURED**, bands never collapse, forecasting never gates.
6. **Byte-identical replay** cloud→laptop.

If all six hold under the live ABB fault loop, Vigil is doing on real infrastructure
exactly what the charter promises — that's the industry-grade bar, not a demo.

---

## 7. Cost, safety, teardown

- **Cost:** `m6i.2xlarge` ≈ $0.384/hr; a campaign + overnight soak ≈ a few dollars.
- **Safety:** the SG opens **only** SSH (22) from your IP. obsd's `:9095` is reached by
  **SSH tunnel** (`ssh -L 9095:localhost:9095 …`) — never a public firewall hole. If it
  must be on a private net, use `--api-token` (deny-by-default on `/api`+`/mcp`).
- **Teardown (do not skip — it bills hourly):**
  `aws ec2 terminate-instances --instance-ids <id>` + delete the SG + key
  (runbook §5A).

---

## 8. What the cloud run proves that the laptop cannot

| | Laptop k3s | AWS m6i.2xlarge |
|---|---|---|
| Pods | capped at 110 | **300+** (`--max-pods=300`) |
| node-PSI / `STORAGE_SATURATION` | unobservable (no node-exporter) | **real** (node-exporter + bounded gp3) |
| Disk-saturation realism | NVMe too fast; coupling had to be forced | **natural** on a bounded network volume |
| Determinism dividend | single machine | **cross-machine** capture→replay |
| Theme alignment | dev box | **single-node industrial edge**, as ABB describes |

The laptop proved the engine is *correct*. The cloud node proves it stays correct at
*industrial scale and realism* — and that the replay guarantee survives leaving the
machine it was captured on.
