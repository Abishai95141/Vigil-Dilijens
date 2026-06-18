# 16 — Cluster Onboarding Guide

> Operator-facing runbook for bringing **Vigil** up on a new Kubernetes cluster.
> Read [`00-master-system-design.md`](00-master-system-design.md) for the *why* and
> [`01-epistemic-separation-charter.md`](01-epistemic-separation-charter.md) for the
> non-negotiable rules. This document is the *how*: prerequisites, bring-up, what you
> will see, how to verify it is correct, and how to read the honest gaps.

This guide reflects Vigil as of the **v4** branch. Every command and `/api` path below
is real and copy-pasteable; nothing here is invented. Where a capability is gated,
deferred, or unobservable on the dev cluster, it is **stated**, not hidden — that is
the whole posture of the product.

---

## 1. Overview — what Vigil gives you, and the promise it keeps

Vigil is Kubernetes-native AI observability built around **one curated, versioned
ontology graph** compiled per cluster into a **bound customer graph**, a
**deterministic detection engine** that reports what is happening *now*, and a
**narrow forecasting clock** (TimesFM, the `clockd` process) that estimates what
crosses a declared bar *soon*.

Three things Vigil **never** does:

- It **never auto-remediates.** It watches; it does not reconcile cluster state. The
  RBAC it asks for is `get`/`list`/`watch` only — no write verbs anywhere
  (`deploy/rbac/clusterrole.yaml`).
- It **never invents causation.** A topological co-occurrence is a co-occurrence; a
  cascade is an *authored* trigger→downstream edge lighting up — never a learned or
  inferred cause.
- It **never invents a bar.** Thresholds come from your own config or the cluster.
  Where nothing is declared, Vigil says so (unbounded, Tier-B-ineligible) instead of
  guessing.

### The epistemic promise (read this before you read any surface)

Every statement-bearing datum carries exactly **one provenance class**, assigned at
birth, immutable:

| Class | Meaning | Example on a surface |
|---|---|---|
| **MEASURED** | A fact read from the store, or a deterministic arithmetic consequence of facts (a threshold state, a rate, a co-occurrence, a match). | "`currencyservice` working-set crossed its 300Mi limit at 14:02." |
| **PROJECTED** | A forecast against a bar, carrying a mandatory uncertainty **band that never collapses to a line**. | "On current trajectory, this crosses its limit in ~9 min (band 4–17 min)." |
| **AUTHORED** | A curated graph relationship, surfaced verbatim with author + version provenance. | "`MEMORY_LEAK` is an authored precursor to `OOM_KILL_CGROUP` (overlay v…, author …)." |

**Join, never fuse.** Classes may be presented adjacently — each labelled — only at
the surfacing layer. No component derives a statement of a stronger class than its
weakest input, and **no model output is ever written into the graph** (there is no
learned edge, weight, or threshold anywhere in the system). When you read a card that
shows a measured fact next to a forecast next to an authored "why," that adjacency is
the join — the three are kept distinct on purpose.

**Detection never waits on forecasting.** The deterministic path produces identical
results whether the clock is present, degraded, or absent.

---

## 2. Prerequisites

### 2.1 What you need before you start

| Prerequisite | Required? | Why |
|---|---|---|
| Cluster access (`--kubeconfig` out-of-cluster, or `--in-cluster` ServiceAccount) | **Yes** | obsd reaches the API server to run the identity layer (doc 03). Without a target it prints a warning and exits — it never invents output. |
| Read-only RBAC (one ClusterRole) | **Yes** for in-cluster | `get`/`list`/`watch` on the watched resources + `get` on `nodes/proxy` for cAdvisor. |
| **cAdvisor** (kubelet, built-in) | **Yes** | Container working-set / CPU / restart signals, reached via the API-server `nodes/proxy` path. No install needed. |
| **kube-state-metrics (KSM)** | Optional | Required for object-state lanes: PVC conditions, restart counters, node/pod conditions. Gated by `--ksm-enabled`. |
| **node-exporter** DaemonSet | Optional | Required for node hardware/kernel signals (MemAvailable, conntrack, PSI, vmstat OOM kills). |
| **conntrack-agent** DaemonSet | Optional (v2 flow lane) | Required for cross-service flow discovery. Gated by `--flow-enabled`. |
| **Events API** (built-in) | Optional | Discrete K8s Events (`OOMKilled`, `CrashLoopBackOff`, image-pull failures). Gated by `--events-enabled`. |

The ontology release ships with Vigil (`ontology/graph/k8s_signal_kg.json` + the
authored overlays in `ontology/graph/overlays`). You do **not** supply it.

### 2.2 Environment constraints — state these up front, never hide them

These are honest standing notes. A surface that reports them is being truthful, not
broken.

- **Dev cluster (`kind`) shares the host kernel.** Container-scoped phenomena
  (working-set → OOM, CPU throttling) are *real*. Node-level memory/IO pressure realism
  is **limited** — stand up a real-VM (k3s) staging cluster before treating the
  node-pressure falsification corpus (doc 11 M3) as authoritative.
  (`deploy/kind/cluster.yaml:5-8`).
- **KSM lane is OFF by default** (`--ksm-enabled=false`). Without it there are no
  object-state streams, no restart counters, and no PVC-condition visibility. The
  released v4 KSM checks are *part of the graph* but stay **unobservable** until you
  enable the scrape. Enabling it does **not** change the digest — it is byte-identical
  to the released ontology; the flag gates only the scrape that makes the checks
  observable (`obsd/cmd/obsd/main.go:105`).
- **PVC fill is unobservable on kind's `local-path` storageClass** — kubelet emits zero
  `kubelet_volume_stats` metrics. PVC **PENDING** *is* observable via KSM. To see
  PVC-fill, use a real cluster / a provisioner that reports volume stats.
- **`container_oom_events_total` is always 0 on kind.** OOM is seen via the **events
  lane** (`reason=OOMKilled`) and pod `lastState.terminated.reason`, not the cAdvisor
  counter. Do not gate coverage decisions on that counter.
- **PSI (Pressure Stall Information) needs kernel ≥ 4.20.** On older kernels PSI
  observations are out-of-scope and marked so in the coverage report.
- **Two workers is the floor, not a nicety.** `runs-on` topology, node-pressure
  phenomena, and the 2-hop noisy-neighbour walk (pod A → node → pod B) are
  unrepresentable on a single node (`deploy/kind/cluster.yaml:1-3`).

---

## 3. Bring-up — step by step

The single command surface is `just`. Run `just --list` for everything.

### 3.1 Stand up the dev cluster (Linux box with Docker)

```bash
just up        # kind create cluster --config deploy/kind/cluster.yaml --name vigil
just rbac      # kubectl apply -f deploy/rbac/clusterrole.yaml (read-only ClusterRole)
just boutique  # deploy Online Boutique + apply the doc 14 §3.3 customizations
```

`just boutique` deploys the Online Boutique into the `online-boutique` namespace and
applies three deliberate customizations so the resolvability-hole policy and a
near-threshold bar are exercised from day one (`justfile:359-381`):

| Service | Customization | Purpose |
|---|---|---|
| `paymentservice` | memory **limit stripped** | Exercises the *unbounded → listed / Tier-B-ineligible* resolvability-hole policy. |
| `recommendationservice` | memory limit tuned to **300Mi** | An honest near-threshold bar (above the ~220Mi request so it stays Ready). |
| `cartservice` | memory limit raised to **256Mi** | Upstream's 128Mi OOMKills the .NET runtime under load (exit 137 / CrashLoopBackOff); 256Mi keeps it green. A declared-limit change, not a removed bar — so it stays Tier-B-eligible. |

The recipe waits for all Deployments to become `Available` (image pulls can take a few
minutes) and prints the pod inventory.

### 3.2 Deploy the optional scrape lanes (as needed)

```bash
kubectl apply -f deploy/workloads/node-exporter.yaml     # node hardware/kernel signals
# kube-state-metrics: install via the pinned helm values under deploy/helm/
# conntrack-agent (v2 flow lane): kubectl apply -f deploy/flow/conntrack-agent.yaml
```

node-exporter is reached the same way as cAdvisor — through the API server's node proxy
(`nodes/<name>:9100/proxy/metrics`); deploying it flips the node-scoped signals from
*out-of-scope* to *obtainable* in the binding report. Tool presence is **detected** from
the workload corpus, never hand-configured (`deploy/workloads/node-exporter.yaml:1-12`).

### 3.3 Run obsd against the cluster

obsd is the runtime core: a single CGO-free Go binary that runs identity, binding,
observation, selection, detection, the unexplained channel, the clock client, and the
`/api` surfaces (`obsd/cmd/obsd/main.go:1-10`).

**Dev (out-of-cluster):**

```bash
just obsd        # runs bin/obsd against your current kubeconfig
# equivalently:
bin/obsd --kubeconfig "$HOME/.kube/config" \
         --store-dir ./_run/store \
         --db ./_run/findings.db
```

**Production (in-cluster):** deploy obsd with the `vigil-obsd` ServiceAccount and pass
`--in-cluster` instead of `--kubeconfig`.

If you pass **no** cluster target, obsd logs `no cluster target — pass --kubeconfig or
--in-cluster` and exits cleanly. There is nothing truthful to report about a live
cluster, so it says exactly that rather than inventing output
(`obsd/cmd/obsd/main.go:133-138`).

### 3.4 Enable the in-cluster lanes (all default OFF; off = byte-identical)

Every lane flag defaults **OFF**, and "off" is byte-identical to the lane not existing —
this preserves the replay/determinism guarantee. Turn on only what your cluster supports
(`obsd/cmd/obsd/main.go:94-105`):

| Flag | Lane | Notes |
|---|---|---|
| `--ksm-enabled` | kube-state-metrics scrape (object-state, restart counters, PVC conditions) | IN-digest; the released v4 checks become observable. |
| `--events-enabled` | Discrete K8s Events joined by CEI (`OOMKilled`, `CrashLoopBackOff`, image-pull) | Rides off the digest. |
| `--app-metrics-enabled` | Application `/metrics` scrape + SLO binding (`vigil.io/slo.*` annotations) | Loads the experimental app-signal overlay on top. |
| `--flow-enabled` | conntrack flow collection (cross-service edges) | Needs the conntrack-agent DaemonSet. |
| `--mcp-enabled` | Read-only MCP harness at `/mcp` | **Advisory only.** Auth is a later track — do not expose beyond an isolated cluster. |
| `--incident-memory` | Durable cross-run incident recurrence counting | Needs `--db` to survive restarts. |
| `--referee-enabled` | `validate_claim` referee (`/api/validate-claim`) | Advisory — checks a claim against the charter + graph; never blocks. |
| `--departure-enabled` | Band-departure anomaly lane (PROJECTED) | Computes off the digest; gate-pending — surfaced only after a live step capture. |

A representative "everything supported on kind" invocation:

```bash
bin/obsd --kubeconfig "$HOME/.kube/config" \
         --store-dir ./_run/store --db ./_run/findings.db \
         --ksm-enabled --events-enabled --app-metrics-enabled \
         --flow-enabled --mcp-enabled --incident-memory --referee-enabled
```

### 3.5 The full obsd flag surface

From `obsd/cmd/obsd/main.go:78-106`:

| Flag | Default | Meaning |
|---|---|---|
| `--kubeconfig` | `""` | Path to a kubeconfig; when set, runs the identity layer against the cluster. |
| `--in-cluster` | `false` | Use in-cluster (ServiceAccount) config to reach the API server. |
| `--params` | `""` | Parameters override file; overlays the embedded dev defaults (doc 14 §5). |
| `--ontology` | `ontology/graph/k8s_signal_kg.json` | Ontology KG release; with a cluster target, enables the binding compiler. |
| `--overlays` | `ontology/graph/overlays` | Authored overlay dir (spans, threshold rules) merged into the ontology. |
| `--releases` | `ontology/releases` | Graph release manifests; the loaded graph self-identifies by content hash. |
| `--store-dir` | `""` | qss warm tier + replay bundle directory; empty = hot rings only (replay capture off, **stated**). |
| `--db` | `""` | SQLite findings database; empty = in-memory (findings reset on restart). |
| `--api` | `true` | Serve the operator `/api` surface on the health server. |
| `--health-addr` | `:9095` | Address for the health/metrics + `/api` server (`/metrics`, `/healthz`, `/readyz`). |
| `--log` | `json` | Log format: `json` \| `text`. |
| `--version` | — | Print version and exit. |
| lane flags | all `false` | See §3.4. |

---

## 4. What you will see

### 4.1 The live entity inventory (the first demo target)

The first observable milestone is "prerequisite zero, observable": obsd, pointed at a
cluster, prints a **live, correctly-joined entity inventory** of everything the identity
layer watches (`obsd/cmd/obsd/main.go:6-9`). The identity layer watches:

- **core/v1:** pods, nodes, namespaces, services, PVCs, PVs, Events
- **discovery.k8s.io:** EndpointSlices (not legacy Endpoints)
- **apps:** ReplicaSets, Deployments, StatefulSets, DaemonSets
- **batch:** Jobs, CronJobs
- **coordination.k8s.io:** Leases (`kube-node-lease`, node liveness)

(`deploy/rbac/clusterrole.yaml:21-45`.)

The **only** join key in the system is the **CEI** (Canonical Entity Identity): exact
match, never fuzzy. Streams that cannot be normalized are **quarantined** — counted and
surfaced — never guessed. The cluster ID is the `kube-system` namespace UID (the one
namespace literal in non-test code, used purely as the cluster anchor).

The Phase-0a join target is ≥ 0.99 coverage with **zero** mis-joins
(`obsd/cmd/obsd/main.go:56-58`).

### 4.2 The `/api` surface (default port `:9095`)

All routes are GET unless noted. From `obsd/internal/api/server.go:88-402`:

| Endpoint | Returns |
|---|---|
| `/api/coverage` | The coverage report (binding states, validation statuses, per-phenomenon observability, caveats). |
| `/api/silence-ledger` | The deterministic absence ledger: every (entity, variable) pair watched-or-silent-with-reason. |
| `/api/incidents` | Durable cross-run incident memory (recurrence counts) — needs `--incident-memory`. |
| `/api/validate-claim` (POST) | The referee verdict for an external claim (advisory) — needs `--referee-enabled`. |
| `/api/events` | Discrete K8s Events joined by CEI — needs `--events-enabled`. |
| `/api/findings` | The persisted findings feed (active / stale by freshness horizon). |
| `/api/unexplained` | Loud-but-unmatched cards + candidate-phenomenon reports + the channel's own blind-spot notice. |
| `/api/insights` | The "now" surface snapshot. |
| `/api/topology` | The topology surface (current marks, edge validity). |
| `/api/warnings` | The early-warning (PROJECTED) surface — serves the honest OFF state behind the gate. |
| `/api/cross-service` | The cross-service cascade surface (v2 flow lane). |
| `/api/root-cause-chain` | The transitive root-cause chain (the one-hop cascade made transitive). |
| `/api/authored-relations` | The curated causal map (authored `phenomenon_relation` edges + vocabulary). |
| `/api/departures` | The band-departure anomaly surface — needs `--departure-enabled`. |
| `/api/timeline` | The composed anomaly timeline (aging, supersede). |
| `/api/context-windows` (POST) | Operator-defined context windows. |
| `/api/chat` (POST) | The register-guarded read-only chat. |
| `/api/config` | Runtime configuration (cadences, graph release, forecast-lane gate posture). |

Quick smoke test once obsd is running:

```bash
curl -s localhost:9095/healthz
curl -s localhost:9095/api/coverage   | head -c 400
curl -s localhost:9095/api/silence-ledger | head -c 400
```

### 4.3 The console operator frontend

`console/` is the TypeScript / React / Vite operator app (pnpm; Cytoscape.js for the
topology graph, ECharts for timeline/series with confidence bands, TanStack Router for
navigation). Its routes (`console/src/router.tsx:25-44`):

| Route | Surface |
|---|---|
| `/` | The cluster graph (the home / headline surface). |
| `/overview` | Cluster overview. |
| `/insights` | The "now" insight feed. |
| `/root-cause` | The transitive root-cause chain (ChainFlow viz). |
| `/events` | The discrete-event lane. |
| `/forecast` | Early warnings (PROJECTED, with confidence bands). |
| `/anomalies` | The anomaly inbox. |
| `/incidents` | Cross-run incident memory. |
| `/timeline` | The anomaly timeline. |
| `/coverage` | The coverage report. |
| `/silence` | The silence ledger. |
| `/referee` | The `validate_claim` referee. |
| `/config` | Runtime config. |
| `/integrations` | The MCP client. **Note:** the page is at `/integrations`, **not** `/mcp` — `/mcp` is proxied to obsd's POST-only JSON-RPC endpoint, so a GET there returns 405 (`console/src/router.tsx:39-42`). |
| `/chat` | The grounded read-only chat. |

---

## 5. How to verify it is correct

Vigil's correctness is *legible*: the coverage report tells you what is and isn't
covered, the silence ledger enumerates every watched pair with its honest reason, and
determinism means a captured replay reproduces byte-identical findings.

### 5.1 Read the coverage report — honest dark bars are EXPECTED

The coverage report (doc 04 §3.5, `obsd/internal/api/coverage.go:15-84`) is the
onboarding deliverable. For every (entity, variable) pair it states:

- **Binding state:** `bound` (collecting), `unresolved` (expected but not
  found/normalized), or `out-of-scope` (capability-excluded).
- **Validation status:** `verified`, `suspect`, or `failed`.
- **Resolvability metrics** and the **unbounded-workload list** (pairs with no
  resolvable bar — these are Tier-B-ineligible / no early warning, but still watched).
- **Per-phenomenon observability:** `full`, `partial`, or `none`.

The report carries **caveats** — the honest standing notes from §2.2 (KSM off, no
`kubelet_volume_stats` on local-path, `container_oom_events_total=0`, PSI kernel
requirement). A pair marked `out-of-scope: PSI not available` or an unbounded
`paymentservice` memory entry is **intentional honesty**, not a defect. Every (entity,
variable) pair has a **visible state** — that is the guarantee.

### 5.2 Read the silence ledger

`/api/silence-ledger` is built from the *same* `binding.Result` as the coverage report
(`obsd/internal/api/server.go:18-22`). It enumerates every pair Vigil *can* watch on
*this* cluster, each marked watched or silent-with-reason. A `silent` entry with a stated
reason is the system telling you the truth about its blind spots — never a bug.

### 5.3 Threshold resolution — where bars come from

Threshold resolution follows strict precedence (doc 04 §3.4): **customer Kubernetes
config → operator override → ontology default** (defaults are flagged). No resolvable bar
means no crossable limit, so the entity is **ineligible for forecasting (Tier B)** and
threshold checks, and is marked **unbounded** in the coverage report. To move a pair from
the unbounded list into Tier A, declare a bar — set a Kubernetes resource limit or an
operator override. (`paymentservice` is unbounded by design after `just boutique` strips
its limit — that is the resolvability-hole policy on display.)

### 5.4 The graph release self-identifies

On load, obsd matches the ontology's content hash against the committed release
manifests in `ontology/releases/`. An unmatched hash is logged as an **UNRELEASED dev
build** — stated, never silently presented as a release
(`obsd/cmd/obsd/main.go:170-182`). Check `/api/config` for the release the running obsd
believes it is on.

### 5.5 Determinism check (the replay guarantee)

Same readings + same graph version + same topology snapshot ⇒ same fingerprints and
matches. A captured replay bundle (Parquet + JSON manifest, written when `--store-dir`
is set) run through obsd twice produces **byte-identical** findings. A non-deterministic
result is a **release-blocking defect**.

### 5.6 The producer-level gates (no live cluster needed)

The `just *-gate` recipes prove detection correctness on frozen corpora — they are how
correctness is certified without a cluster. Examples (`justfile`):

```bash
just ksm-gate            # kube-state-metrics object-state detection
just events-gate         # discrete-event JOIN gate (OOMKilled / CrashLoopBackOff)
just app-slo-gate        # application-SLO phenomena vs declared SLOs
just departure-gate      # band-departure: a sample leaving its own forecast band
just xsvc-gate           # cross-service MEASURED cascade
just validate-claim-gate # the referee
```

### 5.7 Verification checklist

- [ ] `just up && just rbac && just boutique` — pods `Available`.
- [ ] `bin/obsd --kubeconfig …` prints a joined entity inventory, **zero mis-joins**.
- [ ] `curl localhost:9095/healthz` → ok; `/api/coverage` and `/api/silence-ledger`
      return JSON.
- [ ] Coverage report: `paymentservice` memory shows **unbounded** (resolvability hole);
      `recommendationservice` shows a **300Mi** bar.
- [ ] Caveats present and truthful (KSM posture, PVC/local-path, OOM-counter, PSI).
- [ ] Optional lanes you enabled (`--ksm-enabled`, `--events-enabled`, …) report
      *bound*, not *unresolved*, in coverage.
- [ ] Console reachable; `/coverage` and `/silence` mirror the API; MCP page at
      `/integrations` (not `/mcp`).
- [ ] A relevant `just *-gate` PASSES.

---

## 6. The `just` recipes — cluster lifecycle

| Recipe | Does |
|---|---|
| `just up` | `kind create cluster --config deploy/kind/cluster.yaml --name vigil` |
| `just down` | `kind delete cluster --name vigil` |
| `just rbac` | `kubectl apply -f deploy/rbac/clusterrole.yaml` |
| `just boutique` | Deploy Online Boutique + apply doc 14 §3.3 customizations; wait for Available. |
| `just obsd` | Run `bin/obsd` locally against your kubeconfig. |
| `just test` | `go test -race ./...` (hermetic, network-free). |
| `just test-integration` | `go test -race -tags=integration ./...` (needs a kind cluster). |
| `just lint` | go vet + gofmt check + buf lint + graphlint release-immutability check. |

(`justfile:42-70`, `justfile:345-381`.) `just --list` shows the full surface, including
every `*-gate`.

---

## 7. Parameters file and runtime constants

The Parameters file (doc 14 §5) pins the operational constants; obsd loads the embedded
dev defaults and overlays any `--params` file (`obsd/cmd/obsd/main.go:119-131`). The
constants adopted now:

| Constant | Dev value |
|---|---|
| Scrape interval | 15 s |
| Evaluation tick | 15 s |
| Watermark | 30 s |
| Rate window | 5 min |
| Edge staleness budgets (per type) | e.g. `runs-on` 90 s, `selects` 90 s |
| Hot ring capacity | 60 min |
| Warm retention | 7 days |
| Segment size | 2 h |
| Tier-B budget | 50 clock invocations / cycle (dev) |

obsd logs `version`, `profile`, `params_version`, `scrape_interval`, `evaluation_tick`,
and `tier_b_budget` on startup — confirm these match your expectation.

---

## 8. Wired phenomena — what can fire today

The 11 WIRED phenomena that can fire on a supported cluster (doc 00 §3):

`MEMORY_LEAK` · `THROTTLING_CASCADE` · `EVICTION_MEMORY` · `CONNTRACK_EXHAUSTION` ·
`OOM_KILL_SYSTEM` · `STORAGE_SATURATION` · `OOM_KILL_CGROUP` · `PROBE_FAILURE_RESTART` ·
`IMAGE_PULL_FAILURE` · `DISK_PID_INODE_PRESSURE` · `VOLUME_MOUNT_FAILURE`.

Plus **app-level SLO phenomena** (queue-depth, data-staleness, load-surge) vs declared
SLOs (`--app-metrics-enabled`), a **MEASURED cross-service cascade**, and a **PROJECTED
anticipatory cascade** (the v2 flow lane: a leaking callee → TimesFM warns sub-bar → the
anticipatory cross-service cascade fires minutes *before* the MEASURED one confirms).

Several phenomena depend on a lane:
`PROBE_FAILURE_RESTART` and PVC-PENDING object state need `--ksm-enabled`;
`IMAGE_PULL_FAILURE` and the OOM-via-events path need `--events-enabled`;
the app-SLO trio needs `--app-metrics-enabled`. Coverage will show the dependent
phenomena as `none`/`partial` until the lane is on — by design.

---

## 9. Troubleshooting

| Symptom | Likely cause | What to check |
|---|---|---|
| obsd exits immediately with `no cluster target` | No `--kubeconfig` and no `--in-cluster`. | Pass one (`obsd/cmd/obsd/main.go:133-138`). |
| `binding disabled: ontology release not loadable` | Bad `--ontology`/`--overlays` path. | Identity still runs (binding is **non-gating**); fix the path to restore coverage/detection (`obsd/cmd/obsd/main.go:166-167`). |
| Graph logged as "UNRELEASED dev build" | Loaded ontology hash matches no manifest. | Expected on an edited graph; cut a release (`just graph-version`) or accept the dev build (`obsd/cmd/obsd/main.go:174-176`). |
| KSM phenomena (`PROBE_FAILURE_RESTART`, restart counters) show `none` | KSM scrape not enabled, or KSM not discovered. | Pass `--ksm-enabled`; confirm kube-state-metrics is deployed and its `/metrics` is reachable. |
| No PVC-fill, no `kubelet_volume_stats` | kind's `local-path` storageClass emits zero volume stats. | Expected on kind. PVC-PENDING is observable via KSM; PVC-fill needs a real provisioner. |
| OOM happened but `container_oom_events_total` is 0 | That counter is always 0 on kind. | Enable `--events-enabled`; OOM is seen via Events (`reason=OOMKilled`) + pod `lastState.terminated.reason`. |
| PSI observations are `out-of-scope` | Kernel < 4.20. | Expected; PSI needs kernel ≥ 4.20. Marked honestly in coverage. |
| `GET /mcp` returns **405** | `/mcp` is POST-only JSON-RPC. | Use the console page at `/integrations`; the client POSTs to `/mcp` via the proxy (`console/src/router.tsx:39-42`). |
| `/api/incidents` says "not enabled" | `--incident-memory` off, or no `--db`. | Enable the flag **and** pass a persistent `--db` so memory survives restarts (`obsd/cmd/obsd/main.go:97`). |
| `/api/warnings`, `/api/departures`, MCP advisories show the OFF/empty state | The lane is gated behind a backtest gate that has not yet been flipped for live surfacing. | Intentional. The producer still computes off the digest; the surface is withheld until the live gate passes (`obsd/cmd/obsd/main.go:194-197`). |
| A pair is `silent`/`out-of-scope` in the ledger | Capability genuinely unavailable on this cluster. | **This is honesty, not a bug.** The reason string tells you what's missing. |

### A note on the MCP and referee lanes

The MCP interface (`/mcp`, POST-only JSON-RPC) and the `validate_claim` referee
(`/api/validate-claim`) are **advisory**: they validate and label, they never block. Both
are OFF by default and **authentication is a separate, later track** — do not expose them
beyond an isolated cluster (`obsd/cmd/obsd/main.go:96,103`).

---

## 10. Where to go next

- The charter you must internalize: [`01-epistemic-separation-charter.md`](01-epistemic-separation-charter.md).
- Binding, coverage, and the resolvability hole: [`04-binding-and-generalization-engine.md`](04-binding-and-generalization-engine.md) §3.1–3.5.
- The pre-execution constants and kind caveats: [`14-implementation-clarifications.md`](14-implementation-clarifications.md) §3.1, §5.
- The validation/calibration harness and gates: [`11-validation-and-calibration-harness.md`](11-validation-and-calibration-harness.md) §3.5.
- The capability architecture (chains, cascades, app-SLO, anomalies): [`15-capability-architecture.md`](15-capability-architecture.md).

> If anything in this guide disagrees with what your cluster actually reports, **trust
> the surface, not the guide** — the coverage report and the silence ledger are the
> ground truth for what Vigil can and cannot see on *your* cluster, and they are built to
> never lie about it.
