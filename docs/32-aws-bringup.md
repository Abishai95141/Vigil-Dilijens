# 32 — AWS single-node bring-up: Vigil + abb-genix on Ubuntu k3s (all-three substrate)

> Status: RUNBOOK. Target = a brand-new Ubuntu EC2 instance taken to a fully-running
> Vigil (`obsd` + operator console) observing the `abb-genix` reference workload, emitting
> **PSI + container disk-IO + real per-PVC fill** AND everything kind had
> (cAdvisor/KSM/node-exporter/app-metrics/events/logs/traces/audit/flow), with the load
> simulator driving faults. Companion docs: `docs/24` (kind onboarding — the canonical
> obsd run pattern), `docs/31` (the PSI/PVC/IO ingestion plan — the one honest asterisk),
> `docs/14 §3.3` (the mandatory install customizations), `docs/15` (flow + app-SLO).
>
> Every step is copy-pasteable. Assumes **Ubuntu 24.04 LTS + k3s**. Where a step differs
> by CPU arch, both are given — **pick your arch once at the top and stay consistent.**

---

## 1. Goal + substrate verdict

**Goal.** One Ubuntu EC2 instance running:
- `obsd` (the Vigil runtime core) observing a real cluster over its kubeconfig, every lane on;
- the `abb-genix` ABB-Genix-style predictive-maintenance workload (9 pods + monitoring lane);
- the operator console (Vite/React) on `:5173`;
- the load simulator driving faults;
- and emitting **PSI**, **container disk-IO**, and **real per-PVC fill** — the three signals
  no single cluster Vigil has run on so far emitted all at once.

**Substrate verdict — AWS Ubuntu EC2 is a strict SUPERSET of kind.** It gives you, on one node:

| Capability | kind (Mac/LinuxKit) | AWS Ubuntu EC2 k3s | Why better |
|---|---|---|---|
| cAdvisor metrics (working-set, throttle, fs) | ✅ | ✅ | parity |
| **PSI** `container_pressure_*` (6 families) | ✅ (LinuxKit `CONFIG_PSI=y`) | ✅ **native** | Ubuntu kernel ships `CONFIG_PSI=y` + cgroup v2 — no feature gate, no patch |
| container **disk-IO** `container_fs_*` | ✅ | ✅ **real physics** | a real EBS volume, not a Docker overlay — real I/O latency under load |
| **real per-PVC fill** `kubelet_volume_stats_*` | ❌ node-fs-scoped (directory CSI) | ✅ **REAL per-PVC** | EBS CSI (or OpenEBS-LVM) is a block-backed CSI — each PVC reports its own `used/capacity` |
| KSM / node-exporter / app-metrics / events / logs / traces / audit / flow | ✅ | ✅ | parity (same `obsd` flags) |
| generalization proof | — | ✅ | same graph **v0.13.0 hash `6c9e75be`**, identity 100% (proven on Lima/Ubuntu/k3s) |

So: **everything kind could do, plus the two things it could not — native PSI and real
per-PVC fill — on a single box with real I/O physics.** `obsd` is cluster-agnostic Go; it
binds AWS k3s exactly as it binds kind.

**The one honest asterisk.** *Emission* needs no development on AWS — PSI, disk-IO, and
per-PVC stats all ride the existing `FetchCAdvisor → ingestExposition` path and show up
the moment the kernel/CSI are right. But turning those *emitted* signals into Vigil
**coverage** (detection bars that fire, PVC→pod joins, PSI signal nodes in the graph) is
the work of **`docs/31`**, which is **PLAN status, not shipped** on the released graph.
Concretely:
- PSI / PVC-fill / kubelet volume-stats currently have **no authored detection-condition
  nodes** in the released ontology. They will be **scraped and visible as raw streams**,
  but will **not** flip a coverage row to *watched* until the `docs/31` overlay (PSI signals
  + a kubelet `/metrics` volume-stats Fetcher + a PVC→pod CEI join) is authored and released.
- container disk-IO `container_fs_*` is **already scraped** by the cAdvisor fetcher — it is
  inert (no bar bound to it) until an overlay authors one, same as on kind.

Be precise about this when you report results: **this runbook gets you full EMISSION +
parity with kind's ~13 wired phenomena. It does NOT, by itself, add PSI/PVC coverage — that
is the named `docs/31` dev item (see §7).**

---

## 2. Instance spec

### 2.1 EC2 type / arch / AMI

| Tier | Instance | vCPU / RAM | Why |
|---|---|---|---|
| **Floor** | `t3.large` (x86) or `t4g.large` (arm64) | 2 / 8 GiB | abb-genix peaks ~2.9 vCPU-limit / ~3 GiB; k3s + obsd + console add ~1.5 GiB. 4 GiB will OOM under the leak rigs — 8 GiB is the real floor. |
| **Comfortable** | `t3.xlarge` (x86) or `t4g.xlarge` (arm64) | 4 / 16 GiB | headroom for obsd's forecast lane (TimesFM-class memory), the simulator, and concurrent chaos. Recommended for a demo box you'll keep faulting. |

- **AMI:** *Ubuntu Server 24.04 LTS (Noble)* — pick the AMI matching your arch
  (`amd64` for `t3.*`, `arm64` for `t4g.*`). Canonical's official AMIs ship
  `CONFIG_PSI=y` + cgroup v2 by default. **Do not use Amazon Linux** (PSI/cgroup story
  is less predictable for this runbook).
- **ARCH CALLOUT:** the repo's pre-built `deploy/flow/conntrack-agent` binary and the
  `just abb-sim-build` recipe assume **arm64** (they were built on an Apple-silicon Mac).
  - On a `t4g.*` (arm64) box you can reuse the committed binary as-is.
  - On a `t3.*` (x86) box you **must rebuild** `conntrack-agent` for `amd64` and the sim
    image is multi-arch-safe (Python base) — §3 gives both. **Pick arch once.**

### 2.2 EBS layout

Two volumes — this is what unlocks **real per-PVC fill** (the kind blind spot):

| Volume | Size | Mount | Purpose |
|---|---|---|---|
| **Root** | 40 GiB gp3 | `/` | OS + k3s + container images + obsd store (`_run/`). 30 GiB is the bare minimum; 40 gives image-pull headroom. |
| **Data** | 30 GiB gp3 | *raw, unmounted* `/dev/nvme1n1` (or `/dev/xvdf`) | the **dedicated disk** for a block-backed CSI. abb-genix declares **23 GiB of PVCs** (broker 1Gi + historian 10Gi + datalake 10Gi + asset-registry 2Gi), so 30 GiB leaves slack for the fill-rig. |

> **If you use the AWS EBS CSI driver** (recommended, §3.3 option A) you do **not** pre-attach
> a second volume — the driver provisions an EBS volume per PVC dynamically. The 30 GiB
> "data" volume is only needed for the **OpenEBS-LVM** path (§3.3 option B). Default to EBS CSI.

### 2.3 Security group

Default-deny inbound, then allow:

| Port | Proto | Source | Why |
|---|---|---|---|
| 22 | TCP | your IP / SSH | admin + Claude-over-SSH |
| 6443 | TCP | your IP (optional) | k3s API, only if you run `obsd`/`kubectl` from off-box |
| 9095 | TCP | your IP (optional) | obsd `/api` + `/mcp` + `/healthz`, only if you hit it remotely |
| 5173 | TCP | your IP (optional) | console dev server, only if you browse it remotely |

**Recommended pattern:** open only **22**, and reach `:9095`/`:5173` over an **SSH tunnel**
(`ssh -L 9095:localhost:9095 -L 5173:localhost:5173 ubuntu@<ip>`). obsd's `/mcp` has **no
auth** (stated in the flag help) — never expose it on a public SG.

### 2.4 Cost / stop-when-idle

- `t3.large` on-demand ≈ \$0.083/hr; `t3.xlarge` ≈ \$0.166/hr (us-east-1, illustrative).
- EBS gp3 ≈ \$0.08/GiB-month → 70 GiB ≈ \$5.6/mo, **billed even while stopped.**
- **Stop (don't terminate) when idle** to drop compute cost to \$0; EBS persists, k3s + the
  workload come back on start. `obsd` and the console are foreground/`tmux` processes — they
  do not auto-restart; relaunch them after a start (§3.7).

---

## 3. The clean ordered bring-up

Run these on the instance over SSH, as the `ubuntu` user. **Set your arch first:**

```bash
# pick ONE and export it for the whole session
export ARCH=arm64       # for t4g.* instances
# export ARCH=amd64     # for t3.*  instances
export VIGIL=$HOME/vigil # where you clone the repo
```

### 3.1 OS prep

```bash
sudo apt-get update && sudo apt-get install -y git curl docker.io make jq
sudo usermod -aG docker ubuntu && newgrp docker          # docker without sudo (build the sim image)

# install Go 1.26.x (build obsd + conntrack-agent on-box)
GO_VER=1.26.4
curl -fsSL "https://go.dev/dl/go${GO_VER}.linux-${ARCH}.tar.gz" | sudo tar -C /usr/local -xz
echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh && . /etc/profile.d/go.sh
go version                                                # -> go1.26.4 linux/<arch>

# install just (the task runner) + pnpm (the console)
curl --proto '=https' -fsSL https://just.systems/install.sh | sudo bash -s -- --to /usr/local/bin
curl -fsSL https://get.pnpm.io/install.sh | sh - && . ~/.bashrc

# clone the repo
git clone <your-vigil-remote> "$VIGIL" && cd "$VIGIL"

# PSI/cgroup sanity (Ubuntu 24.04 passes all three by default)
uname -r                                                  # >= 6.x
grep -E 'CONFIG_PSI=y' /boot/config-$(uname -r)           # -> CONFIG_PSI=y
mount | grep -q cgroup2 && echo "cgroup v2 OK"
```

### 3.2 Install k3s (single node, control-plane schedulable)

```bash
# k3s >= v1.33 recommended (forward-compatible PSI feature gate; metrics are gate-free
# on this kernel regardless). Embedded containerd is what we import images into.
curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=v1.33.1+k3s1 sh -s - \
  --write-kubeconfig-mode 644 \
  --disable traefik                                       # we don't need the ingress

# make kubectl work for the ubuntu user
mkdir -p ~/.kube && sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config && sudo chown ubuntu ~/.kube/config
export KUBECONFIG=$HOME/.kube/config
echo 'export KUBECONFIG=$HOME/.kube/config' >> ~/.bashrc
kubectl get nodes -o wide                                 # -> 1 node, Ready
```

> Single-node k3s leaves the node tainted control-plane-schedulable-by-default; the
> `just abb-genix` recipe (§3.6) already removes the `NoSchedule` taint and labels the node,
> so no manual taint surgery is needed.

### 3.3 Storage — real per-PVC fill (PICK ONE; recommend option A)

The kind blind spot was directory-backed CSI reporting the node disk for every PVC. You
need a **block-backed CSI** so each PVC reports its own `kubelet_volume_stats_*`.

**Option A — AWS EBS CSI driver (RECOMMENDED on EC2; no second volume needed):**
```bash
# requires the instance IAM role to allow EBS create/attach (ec2:CreateVolume, AttachVolume,
# DescribeVolumes, CreateTags, DeleteVolume) — attach an instance profile with the AWS-managed
# "AmazonEBSCSIDriverPolicy" before launch, OR add an inline policy with those actions.
kubectl apply -k "github.com/kubernetes-sigs/aws-ebs-csi-driver/deploy/kubernetes/overlays/stable/?ref=release-1.35"
# make EBS the default StorageClass so the workload's storageClassName-free PVCs bind it
kubectl apply -f - <<'EOF'
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: ebs-gp3
  annotations: { storageclass.kubernetes.io/is-default-class: "true" }
provisioner: ebs.csi.aws.com
parameters: { type: gp3 }
volumeBindingMode: WaitForFirstConsumer
reclaimPolicy: Delete
EOF
# unset k3s's local-path default so ours wins
kubectl patch storageclass local-path -p \
  '{"metadata":{"annotations":{"storageclass.kubernetes.io/is-default-class":"false"}}}' || true
```

**Option B — OpenEBS-LVM on the dedicated 30 GiB disk (the `docs/31`-proven path; use if
you cannot grant the EBS IAM role):**
```bash
DISK=/dev/nvme1n1                                          # confirm with: lsblk
sudo pvcreate "$DISK" && sudo vgcreate vigilvg "$DISK"
# install OpenEBS LVM LocalPV (per docs/31 §0: set LVM_NAMESPACE on node-DS + controller,
# CSIDriver.storageCapacity=false for single node), then a default SC:
kubectl apply -f https://openebs.github.io/charts/lvm-operator.yaml
kubectl apply -f - <<'EOF'
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: openebs-lvm
  annotations: { storageclass.kubernetes.io/is-default-class: "true" }
provisioner: local.csi.openebs.io
parameters: { storage: lvm, volgroup: vigilvg }
volumeBindingMode: WaitForFirstConsumer
EOF
```

> Either way, the abb-genix manifests are **`storageClassName`-free** by design — they bind
> the **default** SC, which is the cluster-agnostic pattern. Confirm exactly one default:
> `kubectl get sc` should show one `(default)`.

### 3.4 Deploy the monitoring lane (node-exporter + KSM)

```bash
cd "$VIGIL"
kubectl apply -f deploy/workloads/node-exporter.yaml          # DaemonSet :9100, monitoring ns
kubectl apply -f deploy/workloads/kube-state-metrics.yaml     # Deployment :8080 /metrics, monitoring ns
kubectl rollout status -n monitoring ds/node-exporter --timeout=120s
kubectl rollout status -n monitoring deploy/kube-state-metrics --timeout=120s
```
Both are public images (`quay.io/prometheus/node-exporter:v1.9.1`,
`registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.13.0`) — no build. obsd
auto-discovers KSM by image-needle and node-exporter via the node proxy.

### 3.5 Build + import the two private images on-box

k3s uses embedded containerd; load images with `k3s ctr images import` (NOT `kind load`).

```bash
cd "$VIGIL"

# (1) the role-parameterized Python simulator image — public base, builds anywhere
docker build -t vigil-abb-sim:0.1 deploy/workloads/abb-genix/sim

# (2) the conntrack flow agent (Go, static). REBUILD for your arch; the committed binary is arm64.
GOOS=linux GOARCH=$ARCH CGO_ENABLED=0 go build -o deploy/flow/conntrack-agent ./obsd/cmd/conntrack-agent
docker build -t vigil-flow-agent:v0 deploy/flow

# (3) import BOTH into k3s containerd
docker save vigil-abb-sim:0.1 vigil-flow-agent:v0 | sudo k3s ctr images import -
sudo k3s ctr images ls | grep -E 'vigil-abb-sim|vigil-flow-agent'   # -> both present
```

> These are **builds, not development** — the source is complete in the repo (see §5). If a
> build fails it is a toolchain/arch issue, not a code gap.

### 3.6 Deploy abb-genix

The `just abb-genix` recipe does the whole thing on the **current context**: labels +
untaints the node, applies the 3 manifests in order, applies the mandatory `docs/14 §3.3`
customizations (strip `opcua-gateway` limits → resolvability-hole; tune `smart-sensors`
96Mi→64Mi), and waits for the StatefulSet backbone + every Deployment.

```bash
cd "$VIGIL"
just rbac           # read-only ClusterRole/Binding (obsd reads the cluster, never writes)
just abb-genix      # the one-shot deploy + §3.3 customizations + readiness wait
```
Manual equivalent (if you are not using `just`): apply `00-namespace-config.yaml`,
`10-backbone.yaml`, `20-sims.yaml` in that order, then the two `kubectl patch` commands from
`justfile` lines 517–522, then `kubectl wait --for=condition=Available deployment --all -n abb-genix --timeout=420s`.

```bash
just abb-genix-gate # smoke gate: pods Ready, 4 PVCs Bound, historian writing, asset-api serving,
                    # AND the 3-way Vigil contract drift check (metric <-> vigil.io/slo.* <-> overlay)
```

### 3.7 Run obsd with the FULL lane set

Build the binary on-box and run it **out-of-cluster** against the local kubeconfig
(the canonical `docs/24` pattern). Pass flags **inline** (a shell `$VAR` collapses them into
one bad flag).

```bash
cd "$VIGIL"
just build                                            # -> ./bin/obsd ./bin/replay

# enable the forecast/early-warning lane via a params override (docs/24 §2.6)
mkdir -p _run && cat > _run/forecast-on.yaml <<'EOF'
forecast:
  enabled: true
  interval: 30s
EOF

# the onboarding command — every lane on (run in tmux/its own terminal)
./bin/obsd \
  --kubeconfig "$HOME/.kube/config" \
  --health-addr :9095 \
  --db ./_run/abb.db \
  --store-dir ./_run/abb-store \
  --params ./_run/forecast-on.yaml \
  --api --mcp-enabled \
  --ksm-enabled --events-enabled --app-metrics-enabled --flow-enabled \
  --incident-memory \
  --onset-enabled --dgx-enabled --cohypothesis-enabled \
  --assoc-enabled --logs-enabled --referee-enabled --departure-enabled \
  --forecast-role-series
```

> `--logs-enabled` reads pod logs **directly via the kubelet logs subresource** — no Loki,
> no Promtail. `--app-metrics-enabled` discovers pods with `prometheus.io/scrape=true`
> annotations (abb-genix pods opt in) — no exporter to deploy. `--events-enabled` watches
> k8s Events via informer. **These three are turn-key.** The audit and traces SOURCES are
> NOT yet running — wire them in §3.8 if you want those lanes.

### 3.8 Enable the logs / traces / audit SOURCES

- **Logs:** no source to wire — `--logs-enabled` already reads pod stdout/stderr. Done.
- **Audit** (config, k3s-side): k3s apiserver audit logging is OFF by default.
  ```bash
  sudo mkdir -p /etc/k3s
  sudo tee /etc/k3s/audit-policy.yaml >/dev/null <<'EOF'
  apiVersion: audit.k8s.io/v1
  kind: Policy
  rules:
    - level: Metadata
  EOF
  # add the two flags to the k3s server unit and restart
  sudo sed -i 's#server \\#server \\\n        --kube-apiserver-arg=audit-log-path=/var/log/k3s-audit.log \\\n        --kube-apiserver-arg=audit-policy-file=/etc/k3s/audit-policy.yaml \\#' \
    /etc/systemd/system/k3s.service
  sudo systemctl daemon-reload && sudo systemctl restart k3s
  ls -l /var/log/k3s-audit.log                          # -> the JSONL audit log appears
  ```
  Then add to the obsd command: `--audit-enabled --audit-log-path=/var/log/k3s-audit.log`.
- **Traces** (config, needs an OTel file exporter): obsd reads OTel spans as JSONL; it does
  **not** run a collector. Deploy an `otel-collector` with a **file exporter** writing spans
  (one JSON object per line: `traceId/spanId/parentSpanId/service/name/startTime/endTime/error`)
  to e.g. `/var/log/traces.jsonl`, then add `--traces-enabled --traces-path=/var/log/traces.jsonl`.
  Absent the file, the lane degrades silently (no error). abb-genix does not emit OTel spans
  out of the box — this lane is **optional** and is the only "everything kind had" item that
  also wasn't live on kind.

### 3.9 Console + simulator

```bash
# console (over the SSH tunnel, browse http://localhost:5173)
cd "$VIGIL" && just console-dev            # Vite on :5173, proxies /api + /mcp -> :9095

# load simulator (Streamlit) — see §6 for the AWS-specific config it needs
cd "$VIGIL/simulator" && streamlit run app.py
```

### 3.10 Verification

```bash
just psi-preflight                          # PASS = PSI + disk-IO scrapable on the node
                                            #   (PVC-fill WARN only if you skipped a block CSI)
# then the coverage / parity checks in §8
```

---

## 4. Per-lane emission readiness

| Lane | Source | obsd flag | Status on AWS | AWS enablement |
|---|---|---|---|---|
| **cAdvisor metrics** (working-set, CPU throttle, fs) | kubelet `/metrics/cadvisor` via node proxy | (base, always on) | **turn-key** | nothing — scraped on startup |
| **PSI** `container_pressure_*` (6 families) | same `/metrics/cadvisor` | (base, always on) | **turn-key (emit)** / **needs-dev (coverage)** | Ubuntu kernel = `CONFIG_PSI=y` → emits immediately. Coverage = `docs/31` PSI overlay (not shipped). |
| **disk-IO** `container_fs_{reads,writes,io_time}` | same `/metrics/cadvisor` | (base, always on) | **turn-key (emit)** / **needs overlay (coverage)** | already scraped; inert until a bar is authored against it. |
| **PVC fill** `kubelet_volume_stats_*` | `/metrics/cadvisor` (block CSI) | (base, always on) | **needs-config (emit)** / **needs-dev (coverage)** | requires a **block-backed CSI** (§3.3 EBS or LVM). Coverage = the `docs/31` kubelet volume-stats Fetcher + PVC→pod join (not shipped). |
| **KSM** `kube_*` object state | kube-state-metrics pods/proxy | `--ksm-enabled` | **needs-deploy** | `kubectl apply -f deploy/workloads/kube-state-metrics.yaml` (§3.4). Then **fully wired** — `detect-conditions-v4` KSM checks are in the released graph. |
| **node-exporter** `node_*` | node `:9100`/proxy | (scraped when present) | **needs-deploy** | `kubectl apply -f deploy/workloads/node-exporter.yaml` (§3.4). |
| **app-metrics** + declared SLOs | pod `:8080/metrics` (`prometheus.io/scrape`) + `vigil.io/slo.*` | `--app-metrics-enabled` | **turn-key** | no exporter — abb-genix pods opt in via annotations; the app overlay binds the SLO bars. |
| **events** (OOMKilled, CrashLoop, Evicted, FailedScheduling) | k8s Events informer | `--events-enabled` | **turn-key** | nothing — watch on `corev1.Events`. |
| **logs** (deterministic template mining) | pod logs subresource (kubelet) | `--logs-enabled` | **turn-key** | nothing — reads stdout/stderr directly. No Loki/Promtail. |
| **traces** (observed call graph) | OTel spans JSONL file | `--traces-enabled --traces-path=<f>` | **needs-config** | deploy an OTel collector with a **file exporter** writing JSONL (§3.8). Off-digest (candidate-store only). |
| **audit** (change records) | apiserver audit JSONL file | `--audit-enabled --audit-log-path=<f>` | **needs-config** | k3s `--audit-log-path` + `--audit-policy-file` flags + restart (§3.8). Off-digest. |
| **flow** (service-to-service edges) | conntrack-agent `:9111`/proxy | `--flow-enabled` | **needs-deploy** | `kubectl apply -f deploy/flow/conntrack-agent.yaml` after §3.5 build. Edges are CANDIDATE until governance promotes them. |

**Reading the status column:**
- *turn-key* = a flag flips it on; no deploy, no config, no code.
- *needs-deploy* = apply a manifest already in the repo, then the flag works.
- *needs-config* = cluster-side configuration (k3s flags, a CSI, an OTel sink) that the
  operator provides; obsd only reads the result.
- *needs-dev* = obsd code / ontology work that is **not shipped** (the `docs/31` items).

---

## 5. abb-genix deploy checklist

**Images:**
| Image | Source | Action |
|---|---|---|
| `eclipse-mosquitto:2`, `influxdb:2.7`, `minio/minio:RELEASE.2024-09-13`, `postgres:16`, `grafana:11.1.0`, `curlimages/curl:8.10.1`, `quay.io/prometheus/node-exporter:v1.9.1`, `registry.k8s.io/kube-state-metrics:v2.13.0` | **public** | none — pulled on apply |
| `vigil-abb-sim:0.1` | **build** (`deploy/workloads/abb-genix/sim/Dockerfile`, Python 3.12 + paho-mqtt/influxdb-client/psycopg2-binary/minio/requests) | `docker build` + `k3s ctr images import` (§3.5). Used by 5 pods. |
| `vigil-flow-agent:v0` | **build** (`deploy/flow/Dockerfile`, FROM scratch static Go) | rebuild for arch + `docker build` + import (§3.5). Used by the conntrack DaemonSet. |

> **NEEDS BUILDING, NOT DEVELOPING.** Both private images are **fully sourced in the repo** —
> the sim is a complete role-parameterized Python app; the flow agent's Go is complete and a
> pre-built arm64 binary is even committed. There is **zero code to write** to deploy
> abb-genix. The only reason they are not turn-key is that the build artifacts must be
> produced on the target arch and loaded into k3s containerd. If the build errors, it is an
> arch/toolchain mismatch (see §2.1 ARCH CALLOUT), not a missing feature.

**Secrets / ConfigMaps** (all in `00-namespace-config.yaml`, applied first):
- 1 Secret `genix-credentials` (8 keys — broker/historian/datalake/registry creds).
- 5 ConfigMaps: `plant-topology`, `mosquitto-config`, `grafana-provisioning`,
  `grafana-dashboard-provider`, `grafana-dashboard-plant`.
- Grafana admin password is hard-coded `genix` in `10-backbone.yaml` — fine for a demo box;
  override `GF_SECURITY_ADMIN_PASSWORD` if the SG is ever opened.

**Node label / scheduling:** `just abb-genix` auto-labels the single node
`vigil.io/sim-node=abb-genix` and removes the control-plane `NoSchedule` taint (all pods are
single-node-pinned via nodeSelector). No manual labeling.

**Deploy order (enforced by the recipe):**
1. `00-namespace-config.yaml` (namespace + Secret + ConfigMaps)
2. `10-backbone.yaml` (4 StatefulSets w/ volumeClaimTemplates — broker/historian/datalake/registry; headless services)
3. `20-sims.yaml` (5 sim Deployments: smart-sensors, opcua-gateway, stream-processor, pdm-analyzer, asset-api)
4. `docs/14 §3.3` patches: strip `opcua-gateway` limits, set `smart-sensors` memory 64Mi.

**Two by-design holes to expect (not bugs):**
- `opcua-gateway` left **unbounded** → ONE detection bar is intentionally missing
  (Tier-B-ineligible, exercises the resolvability-hole policy).
- `smart-sensors` 64Mi is a tight, honest near-threshold bar (measured RSS ~14Mi) — for
  detection testing, not an OOM risk.

---

## 6. Load simulator

**Robustness verdict: FRAGILE — needs a config fix before it runs on AWS.** The simulator
(`simulator/`, Streamlit) hard-codes the kind context and never validates the cluster:
- `simulator/app.py` defaults context to **`kind-vigil-abb`** and namespace to `abb-genix`.
- `simulator/kube.py` passes `--context` on every `kubectl` call, so a wrong/missing context
  fails **silently** until the first fault injection.
- A `cluster_reachable()` helper **exists but is never called** — there is no preflight, so a
  misconfigured cluster surfaces only as a confusing fault error.

**How to run it on AWS:**
1. The k3s context is named **`default`** (not `kind-vigil-abb`). In the simulator UI, set
   the **context field to `default`** (or whatever `kubectl config current-context` returns)
   and the **namespace to `abb-genix`** before injecting anything.
2. Launch it on the instance and reach it over the SSH tunnel:
   ```bash
   cd "$VIGIL/simulator" && streamlit run app.py --server.address 127.0.0.1
   # then on your laptop: ssh -L 8501:localhost:8501 ubuntu@<ip>  → http://localhost:8501
   ```
3. Confirm it can see the cluster *before* faulting: the UI's Vigil-ready check passes only
   when obsd `:9095` is up, but it does **not** check the cluster — manually run
   `kubectl --context default get ns abb-genix` first.

**Gaps to fix first (small, config-grade — not blockers):**
- Change the `app.py` default context from `kind-vigil-abb` to the value of
  `kubectl config current-context` (or read it dynamically). One-line edit.
- **Call** `cluster_reachable()` at startup and surface a red banner on failure (the function
  is already written — it's just never invoked).
- Alternatively, just override the context/namespace in the UI each session — the simulator
  works as-is once the context string is correct.

Chaos jobs in `corpus/chaos/abb-*.yaml` (e.g. `abb-analyzer-leak.yaml`) are **not** part of
the core deploy — apply them separately to drive specific scenarios, or use the simulator UI.

---

## 7. What needs DEV vs CONFIG vs nothing

**NEEDS NOTHING (turn-key — a flag or an apply, no work):**
- All cAdvisor metrics, **PSI emission**, **disk-IO emission** (base scrape — automatic).
- app-metrics + declared SLOs (`--app-metrics-enabled`), events (`--events-enabled`),
  logs (`--logs-enabled`).
- KSM + node-exporter + flow + abb-genix: **apply a repo manifest** (KSM/node-exporter
  public; sim + flow images are a **build**, not dev — §5).

**NEEDS CONFIG (cluster-side setup; obsd reads the result — no code):**
- **Real per-PVC fill emission** → a block-backed CSI (EBS CSI or OpenEBS-LVM, §3.3).
- **Audit lane** → k3s `--audit-log-path` + `--audit-policy-file` + restart (§3.8).
- **Traces lane** → an OTel collector with a file exporter writing JSONL (§3.8).
- The load simulator context string (§6).

**NEEDS DEV (obsd code / ontology — NOT shipped; the `docs/31` arc):**
- **PSI → coverage:** author PSI signal/detect-condition nodes so `container_pressure_*`
  flips coverage rows to *watched* (today: scraped + visible as raw streams, but unwired).
- **PVC-fill → coverage:** a kubelet `/metrics` volume-stats **Fetcher** (the current
  fetcher reads `/metrics/cadvisor` only; per-PVC stats also live on kubelet `/metrics`) +
  a **PVC→pod CEI join** + a fill detect-condition. This is the substantive `docs/31` build.
- **disk-IO → coverage:** an authored bar against `container_fs_*` (minor — the stream is
  already ingested).
- (Pre-existing, not new on AWS) the **flow Fetcher integration** is incomplete — `--flow-enabled`
  asserts edges via the agent, but verify edge count `>0` in the obsd log; flow edges remain
  CANDIDATE/off-digest until governance promotes them.

> The headline: **AWS gets you 100% emission + full kind-parity coverage (~13 phenomena)
> with zero dev.** The *only* dev left is turning the two new emitted signals (PSI, PVC) into
> new coverage — and that work is already specified in `docs/31`.

---

## 8. Verification gates

Run these in order. They prove **all three new signals emit**, **coverage reaches kind
parity**, **the dependency graph bound**, and **the simulator works**.

**A. All-three emission (the kind blind spots, now live):**
```bash
just psi-preflight                                  # expect: PSI PASS, disk-IO PASS
# raw proof straight off the kubelet (run on the node):
N=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
kubectl get --raw "/api/v1/nodes/$N/proxy/metrics/cadvisor" \
  | grep -cE '^container_pressure_'                 # PSI families  -> >0
kubectl get --raw "/api/v1/nodes/$N/proxy/metrics/cadvisor" \
  | grep -cE '^container_fs_(reads|writes)_bytes_total'   # disk-IO  -> >0
# REAL per-PVC (the kind blind spot): each PVC must report its OWN capacity, not the node disk
kubectl get --raw "/api/v1/nodes/$N/proxy/metrics/cadvisor" \
  | grep -E '^kubelet_volume_stats_capacity_bytes' | sort -u | head
#   -> several DISTINCT capacities (1Gi/10Gi/10Gi/2Gi), NOT one repeated node-disk figure
```

**B. abb-genix wired + flowing:**
```bash
just abb-genix-gate            # pods Ready, 4 PVCs Bound, historian writes>0, asset-api serves,
                               # 3-way Vigil contract drift check PASSES
kubectl get pods,pvc -n abb-genix -o wide
```

**C. obsd bound the cluster + coverage parity (~13 phenomena, kind-full):**
```bash
curl -s localhost:9095/readyz                                              # -> 200
# obsd ingest line in its log should read: cadvisor:1 node-exporter:1 app:N ksm:1 (all four lanes)
curl -s localhost:9095/api/topology | jq '{nodes:(.nodes|length), edges:(.edges|length)}'
curl -s localhost:9095/api/coverage | jq '{graph:.graphVersion, available:.available}'
#   -> graph "v0.13.0" (hash 6c9e75be substrate-independent), available count == kind's
curl -s localhost:9095/api/silence-ledger | jq '.summary | {pairs:.totalPairs, watched, silent}'
# count fired phenomena (the ~13-full parity check): should match the kind baseline
curl -s localhost:9095/api/insights | jq '[.[].phenomenon] | unique | length'
```

**D. The dependency graph (flow + topology bound):**
```bash
kubectl apply -f deploy/flow/conntrack-agent.yaml                          # if not already
# obsd log should print: flow collector: observed-flow edges asserted edges=N  (N>0)
curl -s localhost:9095/api/dependency | jq 'length'                        # assoc edges (>0 once warmed)
curl -s localhost:9095/api/root-cause-chain | jq '.'                       # transitive chain over flow edges
```

**E. The MCP relay (parity surface):**
```bash
curl -s -X POST localhost:9095/mcp -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq '.result.tools | length'   # -> 27
```

**F. The simulator (after the §6 context fix):**
```bash
kubectl --context default get ns abb-genix                                 # reachable -> Active
# in the UI: set context=default, namespace=abb-genix, inject a leak fault, then watch
curl -s localhost:9095/api/insights | jq '.[].phenomenon' | sort | uniq -c # the fault should surface
```

**PASS when:** A shows PSI+disk-IO+distinct-per-PVC; B's gate prints `PASSED`; C's coverage
graph is `v0.13.0` with `available` ≥ the kind baseline and fired-phenomenon count ≈ 13; D
shows flow edges `>0` and a non-empty root-cause chain; E lists 27 tools; F surfaces an
injected fault. That is **kind-parity + the two AWS-only emission wins, fully verified.**
