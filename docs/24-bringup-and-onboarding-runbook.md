# 24 — Bring-up & Cluster-Onboarding Runbook (reproducible, from zero)

> Status: EXECUTED end-to-end on macOS + kind (branch `v6`, 2026-06-21).
> This is the **single, copy-pasteable** path from a clean machine to a fully live
> Vigil observing a Kubernetes cluster. Every command here was run as written — no
> manual patching, no hidden steps. Companion: `docs/16` (operator onboarding context),
> `docs/23` (the MCP operator-parity surface), `docs/25` (the incident simulation).

## 0. Prerequisites (host tools)

Verified versions on the reference machine (any reasonably recent build works):

| Tool | Version used | Why |
|---|---|---|
| Docker | 29.4.0 | kind's container runtime + sim image build |
| kind | 0.31.0 | local Kubernetes cluster |
| Go | 1.26.4 | build `obsd` (CGO-free) |
| uv | 0.9.29 | `clockd` (TimesFM) + harness Python envs |
| pnpm | 10.33.0 | the operator console (Vite/React) |
| kubectl | bundled w/ kind context | cluster access |

Everything is driven through **`just`** (the single command surface). `just --list`
shows all recipes.

---

## 1. Teardown — return to a completely fresh state

Kill every Vigil service, then delete every kind cluster. (Adjust the cluster names to
whatever `kind get clusters` reports.)

```bash
# 1a. services
pkill -f 'bin/obsd'      || true      # the runtime core
pkill -f 'go run.*obsd'  || true
pkill -f 'pnpm dev'      || true      # the console dev server
pkill -f 'node.*vite'    || true
pkill -f 'clockd.server' || true      # the forecasting service

# 1b. clusters (delete each one kind reports)
for c in $(kind get clusters); do kind delete cluster --name "$c"; done

# 1c. verify fresh
kind get clusters                      # -> "No kind clusters found."
lsof -nP -iTCP:9095 -sTCP:LISTEN        # -> empty (obsd)
lsof -nP -iTCP:50051 -sTCP:LISTEN       # -> empty (clockd)
lsof -nP -iTCP:5173 -sTCP:LISTEN        # -> empty (console)
```

---

## 2. Bring-up — cluster, workload, services (in order)

> Each step was run top-to-bottom. Wall-clock on a warm machine (images cached): the
> whole section ≈ 4–6 minutes; the first ever run is longer (image pulls + TimesFM
> checkpoint download).

### 2.1 Create the dedicated single-node cluster

```bash
just abb-up                            # kind create cluster --config deploy/kind/abb-cluster.yaml --name vigil-abb
```
Sets the kube-context to `kind-vigil-abb`. The node reports `NotReady` for ~20–30s
while the CNI installs, then `Ready` — wait for it before the next step:
```bash
kubectl wait --for=condition=Ready node --all --timeout=120s
```

### 2.2 Read-only RBAC + build/load the simulator image

```bash
just rbac                              # ClusterRole/Binding: obsd reads the cluster, never writes
just abb-sim-build vigil-abb           # docker build the sim image + `kind load` it into THIS cluster
```
Note the **positional** cluster arg (`vigil-abb`), not `cluster=vigil-abb`.

### 2.3 Deploy the ABB Genix simulation

```bash
just abb-genix                         # apply backbone + sims, pin to the node, wait for Available
```
Brings up the stateful backbone (`edgenius-broker`, `genix-historian`, `genix-datalake`,
`asset-registry` — 4 PVCs) and the app tier (`smart-sensors`, `opcua-gateway`,
`stream-processor`, `pdm-analyzer`, `asset-api`, `operations-dashboard`). Ends with all
Deployments `Available` and 4 PVCs `Bound`.

### 2.3b Deploy the telemetry exporters (KSM + node-exporter)

The cluster ships no metrics stack; Vigil scrapes these two exporters for the object-state
(KSM) and node lanes. **Order matters** — the node-exporter manifest creates the
`monitoring` namespace that KSM expects:

```bash
kubectl apply -f deploy/workloads/node-exporter.yaml      # creates ns/monitoring + node-exporter DaemonSet
kubectl apply -f deploy/workloads/kube-state-metrics.yaml  # KSM Deployment in ns/monitoring
kubectl rollout status ds/node-exporter -n monitoring --timeout=180s
kubectl rollout status deploy/kube-state-metrics -n monitoring --timeout=180s
```
After this, obsd's ingest line reads `cadvisor:1 node-exporter:1 app:N ksm:1` (all four
families). Without it: `node-exporter:0 ksm:0` and the KSM/node phenomena stay dark.

### 2.3c Deploy the flow-agent (observed service-to-service edges)

The cross-service cascade and the transitive root-cause chain are built from **observed
flow** (who actually talks to whom). obsd reads each node's conntrack table via the
API-server node proxy at `nodes/<n>:9111/proxy/conntrack`; a one-per-node DaemonSet
exposes it. Build it once, load it, apply it (the build line is in `deploy/flow/Dockerfile`):

```bash
# kind node arch = your Docker host arch (arm64 on Apple Silicon, amd64 on Intel/Linux)
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o deploy/flow/conntrack-agent ./obsd/cmd/conntrack-agent
docker build -t vigil-flow-agent:v0 deploy/flow/
kind load docker-image vigil-flow-agent:v0 --name vigil-abb
kubectl apply -f deploy/flow/conntrack-agent.yaml
kubectl rollout status ds/conntrack-agent -n monitoring --timeout=120s
```
After this, obsd logs `flow collector: observed-flow edges asserted edges=N` with `N>0`
(≈12 for the abb-genix mesh), and `/api/cross-service` reports `enabled: true`. Without
it, obsd logs `fetch conntrack ... 9111 ... unable to handle` and asserts 0 flow edges —
the cascade/chain lanes have nothing to walk.

### 2.4 Smoke-gate the workload (proves data is flowing)

```bash
just abb-genix-gate
```
Asserts every pod Ready, 4/4 PVCs Bound, the historian is **receiving writes**, the
asset-api serves assets, **and** the Vigil SLO contract has not drifted (the emitted
metric name ⇄ the `vigil.io/slo.*` annotation ⇄ the overlay rule all agree for
`app_requests_total`, `app_queue_depth`, `app_last_update_seconds`). Prints
`ABB Genix gate PASSED`.

### 2.5 Start the forecasting service (clockd / TimesFM)

```bash
just clockd timesfm                    # gRPC clock on :50051 — POSITIONAL arg (NOT CLOCK=timesfm)
```
Run it in its own terminal (or backgrounded). First run loads the TimesFM checkpoint
(slow once, then cached). `obsd` never gates on it (the charter's non-gating rule):
detection is identical whether the clock is up, degraded, or absent — the clock only
adds the PROJECTED early-warning lane.

> **Gotcha (documented):** `just` recipe parameters are **positional**. `just clockd
> timesfm` is correct; `just clockd CLOCK=timesfm` passes the literal string
> `CLOCK=timesfm` to `--clock` and exits 2.

### 2.6 Build + run obsd against the cluster (the onboarding — see §3)

```bash
just build                             # version-stamped ./bin/obsd + ./bin/replay
```

Forecast is opt-in via a params override (declared, not hidden):
```bash
mkdir -p _run
cat > _run/forecast-on.yaml <<'EOF'
forecast:
  enabled: true
  min_context: 8        # forecast after ~2min of history (default 64 ≈ 16m)
  interval: 30s         # faster forecast cycles (default 60s)
EOF
```

Run obsd with every lane on (this is the onboarding command — §3 explains each flag):
```bash
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
Run it backgrounded or in its own terminal. `--kubeconfig "$HOME/.kube/config"` works
because `just abb-up` already set the current context to `kind-vigil-abb`.

> **Shell note:** pass the flags **inline** (as above). In zsh, an unquoted
> `$VAR="--a --b"` is NOT word-split, so collapsing the flags into a variable makes obsd
> see one giant flag and exit 1. Inline (or `bash -c`) avoids it.

### 2.7 Start the operator console

```bash
just console-dev                       # Vite dev server on http://localhost:5173 (proxies /api + /mcp to :9095)
```

---

## 3. Standardized cluster onboarding (the definitive process)

**Onboarding = pointing obsd at a cluster's kubeconfig and letting the identity layer
bind it.** obsd runs **out-of-cluster** here (reading the API server over the kubeconfig);
the read-only RBAC of §2.2 is what an *in-cluster* deployment would use. There is no
agent installed in the workload — Vigil observes, never injects.

The single onboarding command is the obsd invocation in §2.6. What each flag does:

| Flag | Lane it turns on |
|---|---|
| `--kubeconfig <path>` | the cluster to observe (identity layer binds entities here) |
| `--health-addr :9095` | the health/metrics + `/api` + `/mcp` server |
| `--db`, `--store-dir` | durable findings + the time-series store (survive restart) |
| `--params _run/forecast-on.yaml` | enable the forecast/early-warning lane |
| `--api --mcp-enabled` | the operator surfacing API + the 27-tool MCP relay (doc 23) |
| `--ksm-enabled` | kube-state-metrics object lane (restarts, conditions) |
| `--events-enabled` | discrete k8s Events (OOMKilled, CrashLoopBackOff) |
| `--app-metrics-enabled` | scrape app `/metrics` + bind `vigil.io/slo.*` declared SLOs |
| `--flow-enabled` | observed service-to-service flow edges (topology/cascade) |
| `--incident-memory` | durable cross-run recurrence counting |
| `--onset-enabled` | MEASURED changepoint timing (C2) |
| `--dgx-enabled` | the firewalled candidate store + provisional-coverage/governance |
| `--cohypothesis-enabled` | direction-free co-onset leads (C3) |
| `--assoc-enabled` | MEASURED metric-association (associated-with) edges |
| `--logs-enabled` | deterministic log-template mining |
| `--referee-enabled` | the `validate_claim` honest-labeler |
| `--departure-enabled` | band-departure anomaly lane (gate-pending surface) |
| `--forecast-role-series` | churn-stable role forecasting (survives pod churn) |

### 3.1 Verify the onboarding succeeded

```bash
# health
curl -s localhost:9095/readyz                      # -> 200

# the identity layer bound the cluster's entities (live inventory)
curl -s localhost:9095/api/topology | python3 -c 'import sys,json;d=json.load(sys.stdin);print("nodes",len(d["nodes"]),"edges",len(d["edges"]))'

# every (entity,variable) pair is watched-or-silent with a reason (honest coverage)
curl -s localhost:9095/api/coverage | python3 -c 'import sys,json;d=json.load(sys.stdin);print("graph",d.get("graphVersion"),"available",d.get("available"))'
curl -s localhost:9095/api/silence-ledger | python3 -c 'import sys,json;d=json.load(sys.stdin);s=d["summary"];print("pairs",s["totalPairs"],"watched",s["watched"],"silent",s["silent"])'

# the MCP relay advertises all 27 tools
curl -s -X POST localhost:9095/mcp -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  | python3 -c 'import sys,json;print(len(json.load(sys.stdin)["result"]["tools"]),"MCP tools")'
```

A successful onboarding shows: a non-empty topology (the cluster's workloads/nodes/
services bound), a coverage report whose `graphVersion` matches the release, a silence
ledger that accounts for every pair, and 27 MCP tools. (Live numbers for this run are in
§4.)

---

## 4. Verified live state (this run)

Brought up from a fully clean machine (no clusters, no services), commands exactly as
above. Live results:

| Check | Result |
|---|---|
| kind cluster | `vigil-abb` (1 node, Ready) |
| abb-genix workload | 10 pods Running, 4/4 PVCs Bound, `ABB Genix gate PASSED` (1080 historian writes) |
| telemetry | obsd ingest `cadvisor:1 node-exporter:1 app:6 ksm:1`, ~1800 streams |
| flow edges | `observed-flow edges asserted edges=12` (the abb-genix mesh) |
| clockd | `clockd serving clock=timesfm on 127.0.0.1:50051` |
| obsd | `:9095/readyz` → 200 |
| console | Vite on `http://localhost:5173` |
| topology | 37 nodes / 36 structural edges bound |
| coverage | available, every (entity,variable) pair accounted |
| silence ledger | 253 pairs = 142 watched + 111 silent (each with a reason) |
| MCP relay | 27 tools advertised |
| fully-observable phenomena | APP_DATA_STALENESS, APP_LOAD_SURGE, APP_QUEUE_SATURATION, OOM_KILL_CGROUP, PROBE_FAILURE_RESTART, DISK_FILLING, CONNTRACK_EXHAUSTION, HPA_FLAPPING, INIT_CONTAINER_FAILURE, … |

System healthy at rest: 0 findings (no incident injected yet). The incident simulation
that exercises all of this is `docs/25`.

### 4.1 Full bring-up command digest (copy-paste, in order)

```bash
# clean slate
for c in $(kind get clusters); do kind delete cluster --name "$c"; done
# cluster + workload
just abb-up && kubectl wait --for=condition=Ready node --all --timeout=120s
just rbac && just abb-sim-build vigil-abb && just abb-genix && just abb-genix-gate
# telemetry + flow
kubectl apply -f deploy/workloads/node-exporter.yaml
kubectl apply -f deploy/workloads/kube-state-metrics.yaml
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o deploy/flow/conntrack-agent ./obsd/cmd/conntrack-agent
docker build -t vigil-flow-agent:v0 deploy/flow/ && kind load docker-image vigil-flow-agent:v0 --name vigil-abb
kubectl apply -f deploy/flow/conntrack-agent.yaml
# services
just clockd timesfm            # terminal 1 (or backgrounded)
just build && ./bin/obsd --kubeconfig "$HOME/.kube/config" --health-addr :9095 \
  --db ./_run/abb.db --store-dir ./_run/abb-store --params ./_run/forecast-on.yaml \
  --api --mcp-enabled --ksm-enabled --events-enabled --app-metrics-enabled --flow-enabled \
  --incident-memory --onset-enabled --dgx-enabled --cohypothesis-enabled \
  --assoc-enabled --logs-enabled --referee-enabled --departure-enabled --forecast-role-series   # terminal 2
just console-dev               # terminal 3 → http://localhost:5173
```

