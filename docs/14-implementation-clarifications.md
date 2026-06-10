# 14 — Implementation Clarifications & Pre-Execution Decisions

**Role in suite:** The bridge from blueprint (00–13) to first commit. This document answers the four pre-execution questions (edge-churn parameters, store persistence, reference cluster, exporter dialects/workload), then audits the blueprint for everything an implementer would find ambiguous, incomplete, or undecided — with a recommended initial answer for each. All constants adopted here are **initial values**: they live in one versioned parameters file and are revised by harness evidence (11), per the blueprint's open-question ledgers.

---

## 1. Question 1 — Edge staleness budgets and tombstone retention

### 1.1 Confirmation mechanics first, budgets second

A budget only means something relative to how often an edge can be confirmed. Three confirmation channels, in priority order:

1. **Watch events** (primary): the client-go informer stream updates `last-confirmed-at` on any event touching an edge's endpoints (pod bound, EndpointSlice updated, PVC attached). Near-real-time when flowing.
2. **Periodic reconciliation** (backstop): a full informer-cache sweep comparing the edge store against the live cache. **Initial interval: 30 s (dev) / 5 min (production default).**
3. **Confirm-on-demand probe** (the mechanism that keeps budgets tight without relist storms): when an edge approaches its budget *and* a detection walk wants to traverse it, issue one targeted live read of the relevant object. Only if the probe fails or is unavailable does the edge become **suspect**. Suspect ≠ retracted: a suspect edge degrades a match (07 §3.2); it never supports a full one.

### 1.2 Initial staleness budgets (per edge type)

Formula: **budget = 3 × effective confirmation interval**, where the effective interval is the watch cadence when events are flowing, else the reconciliation interval. Dev values assume the 30 s reconcile:

| Edge type | Confirmed by | Initial budget (dev) | Rationale |
|---|---|---|---|
| `runs-on` (pod → node) | Pod informer events + reconcile | **90 s** | Binding is immutable for a pod's life; it changes only via lifecycle events we also watch, so 3 missed reconciles ⇒ informer trouble ⇒ suspect is correct |
| `selects` (service → pod) | EndpointSlice informer + reconcile | **90 s** | Fastest-churning edge; EndpointSlice events confirm aggressively under any traffic change |
| `mounts` (pod → PVC) | Pod-spec informer + reconcile | **120 s** | Changes only with pod lifecycle; slightly relaxed |
| `owns` / controls (pod → ReplicaSet → Deployment) | OwnerReference watch | **600 s** | Immutable in practice for the child's lifetime; cheap to confirm at birth |
| Node membership / liveness | **Lease watch** (`kube-node-lease`, ~10 s renew) | **40 s** | Deliberately mirrors the control plane's own node-monitor grace period, so our "node suspect" aligns with Kubernetes' own judgment |

Production: same formula with the 5 min reconcile, *plus* the confirm-on-demand probe, which keeps the effective suspicion latency at probe latency rather than 15 minutes. All budgets are per-edge-type entries in the parameters file; ledger item #1 (13 §6) closes when reference-cluster churn data replaces these with measured values.

### 1.3 Retracted-edge retention (the part churn actually stresses)

Detection walks must intersect edge validity with the evaluation window (03 §3.5), so **retracted edges must remain queryable for as long as any evaluation window can reach back**:

- **In-memory horizon for retracted edges: 30 min** = max co-occurrence window (initial 15 min, see §7) + late-sample watermark + slack.
- Beyond that, every edge assertion (asserted / confirmed / retracted, with timestamps) is appended to an **on-disk topology log** — which is not optional bookkeeping: it is the topology-snapshot component of replay bundles (11 §3.1). Retention follows the warm store (7 days, §2).

### 1.4 Tombstone retention — two tiers plus a safety valve

Purpose (03 §3.4): late-arriving samples must join the correct, now-dead instance; role succession must stay reconstructible.

| Tier | Contents | Retention | Why this number |
|---|---|---|---|
| **Full tombstone** | Complete identity record: CEI, all coordinates, lifecycle timestamps, role CEI, predecessor/successor | **15 min** | Covers the worst realistic sample lateness: scrape interval (15–30 s) + ingest pipeline delay + the 5 min staleness semantics Prometheus-style pipelines carry, doubled with margin |
| **Succession stub** | CEI, role key, born/died, successor CEI (~120 bytes) | **24 h** in memory; then archived to the identity log on disk | Keeps role continuity ("does this service OOM every few hours") answerable across a full day of churn without raw-record weight |

**Memory math under pathological churn** — at a deliberately absurd 10 pod replacements/second: full tombstones = 10 × 900 s × ~400 B ≈ **3.6 MB**; stubs at 1 pod/s for 24 h = 86,400 × 120 B ≈ **10 MB**. Memory bloat is therefore not a real risk at these horizons; the protection that *does* matter is the safety valve: a **hard cap of 250,000 tombstone entries with LRU eviction**, where every early eviction increments a published health metric (`tombstones_evicted_before_horizon`). Evictions firing is itself a join-risk signal (03 §6) and surfaces in layer health — never silently.

Late samples arriving after the full-tombstone window join the stub (correct CEI, degraded detail) and are counted; samples matching nothing quarantine as usual.

## 2. Question 2 — Store persistence: in-memory rings vs disk spill

### 2.1 Verdict

**Pure in-memory concurrent ring buffers are sufficient — and are the permanent design — for the hot path.** Fingerprint evaluation (05) needs minutes of history, never days, and should *never* touch disk; that is worth keeping as a standing invariant, not a Phase-0 shortcut.

**Disk persistence is nonetheless required from Phase 0b**, for two reasons that have nothing to do with the hot path:

1. **The replay substrate (11 M1) requires recorded readings.** Capturing telemetry to disk is a Phase 0b deliverable regardless of any storage debate — the sealed files *are* the readings component of replay bundles.
2. **Phase 2 forecast context windows need days, not minutes.** TimesFM 2.5 accepts up to 16,384 context points: at 15 s cadence that is ~2.8 days of history per target, ~5.7 days at 30 s. Holding the warm window purely in memory is feasible on a dev box but fragile, and a restart would silence Tier B for days while context refills — unacceptable.

### 2.2 On `tstorage` specifically

`nakabonne/tstorage` does what its README says (in-memory partitions with optional disk persistence via a data path, Gorilla-style encoding) and its design write-up is genuinely useful — but the project has been dormant since roughly 2023, with no maintenance signal since. **Decision: use it as a design reference, not a dependency.** Adopting an unmaintained storage engine under a trust-first product is the wrong trade.

### 2.3 Recommended architecture: own a deliberately thin store

The access pattern is unusually narrow — append by (CEI, variable); read contiguous tail windows; delete by age — which makes a bespoke store *smaller* than the integration glue for a general-purpose TSDB:

| Layer | Mechanics | Notes |
|---|---|---|
| **Hot ring** (in memory) | One fixed-capacity ring per (CEI, variable); lock-light concurrent append; tail reads for fingerprints | **60 min** capacity initial; the only thing the hot path ever reads |
| **Warm segments** (on disk) | Ring contents flushed into append-only **2 h segment files** per shard; sealed segments are immutable; crash recovery = replay the unsealed segment | Plain fixed-width records (timestamp 8 B + value 8 B) first — debuggable, agent-friendly; Gorilla/delta-of-delta compression is a later milestone, not a prerequisite |
| **Retention** | Delete segments older than **7 days** (covers the maximum TimesFM context with margin) | Background job; retention is a parameter |
| **Bundle export** | Sealed segments + the topology log + graph version + bar set + parameters export to **Parquet + JSON manifest** | Parquet deliberately: the harness's backtest and falsification analytics run in Python, and columnar reads bridge the Go runtime to the Python analysis world with zero custom parsing |
| **Durability policy** | Batched fsync every **1 s** on the active segment | Losing ≤1 s of telemetry on crash is acceptable; the rings repopulate from live scrape immediately |

**Sizing sanity check** (dev cluster, ~3,000 bound streams at 15 s): hot rings = 240 points × 16 B × 3,000 ≈ **12 MB RAM**; warm 7 d uncompressed = 40,320 points × 16 B × 3,000 ≈ **1.9 GB disk** (≈250 MB once Gorilla lands). A 10× larger cluster stays comfortably on one node.

**Fallback if the bespoke path stalls:** embed the Prometheus TSDB Go library (proven WAL/compaction/head-block machinery, used by Mimir/Thanos receivers) behind the same store interface. Rejected as the *first* choice only because its label-model and compaction surface vastly exceed the access pattern. Rejected outright: running an external Prometheus/VictoriaMetrics as the system's store — the architecture requires CEI-stamped, canonically named streams at ingest (03/05), and outsourcing storage to the very ecosystem whose dialects we normalize muddles the identity join the whole design rests on.

## 3. Question 3 — Reference cluster, exporter dialects, and workload

### 3.1 Development cluster

**Primary: kind, 3 nodes (1 control-plane + 2 workers), via a checked-in config and a local kubeconfig.** Two workers is the floor, not a nicety: `runs-on` topology, node-pressure phenomena, and the 2-hop noisy-neighbour walk (pod A → node → pod B) are unrepresentable on a single node. kind is fully scriptable (create/destroy in ~1 min), is the CI standard (runs inside GitHub Actions), and needs nothing but Docker — ideal for an agent-driven loop. k3d is an acceptable lighter alternative; Minikube works but multi-node scripting is clumsier.

Cluster add-ons installed by the dev bootstrap: **kube-state-metrics** (Helm), **metrics available from kubelet's cAdvisor endpoint** (built-in; reachable in dev via the API-server proxy path per node, which avoids node-network plumbing), **node-exporter** (Helm, for node hardware signals), and later **Chaos Mesh** (Helm — it deploys cleanly on kind).

**Honest caveat:** all kind "nodes" share the host kernel. Container-scoped phenomena (working-set → OOM kill, CPU throttling) are real, because cgroup limits are per-container; *node-level* memory/IO pressure realism is limited. This is fine for Phases 0a–0b (identity, informers, binding, entity-local detection) and most of Phase 1; before the falsification corpus (11 M3) is treated as authoritative for node-pressure phenomena, stand up a **staging cluster of 3 small real VMs running k3s** (or a minimal managed-K8s dev cluster). Plan it as a Phase-1 line item, not a blocker today.

### 3.2 Exporter dialects — yes, with the exact label map

Assume **kube-state-metrics + cAdvisor as the two canonical dialects** for the first normalization maps (03 M2), with **node-exporter** third and **OTel semantic conventions added as the fourth lane in Phase 0b–1** — added deliberately *after* the first two work, to prove the normalization machinery is not single-dialect-shaped. We control the dev cluster, so no custom collector surprises exist until a real customer brings one.

The dialect facts the normalization maps must encode (and the join honeypot inside them):

| Source | Identity labels carried | What it uniquely provides | Trap to encode |
|---|---|---|---|
| **cAdvisor** (kubelet `/metrics/cadvisor`) | `namespace`, `pod`, `container`, `id` (cgroup path), `image` | The numbers themselves: `container_memory_working_set_bytes`, CPU usage/throttling, fs usage | **No pod UID label.** Also `container=""` rows are pod-level cgroup aggregates and `container="POD"` is the pause sandbox — both must map to the pod CEI or be dropped deliberately, never bound to a container CEI |
| **kube-state-metrics** | `namespace`, `pod`, **`uid`** (`kube_pod_info`), `owner_kind`/`owner_name` (`kube_pod_owner`) | The identity backbone: UID for CEI minting, ownership chain for **role derivation** (03 §3.1), and **`kube_pod_container_resource_limits{resource,unit}` — the threshold-resolution source for borrowed normativity (04 §3.4)** | State metrics persist briefly for terminated pods; lifecycle states must gate binding |
| **node-exporter** | `instance`/node | Node hardware signals beyond kubelet's view | Node name ↔ Node object mapping is the only join needed |
| **OTel semconv** (Phase 0b–1) | `k8s.pod.uid`, `k8s.pod.name`, `k8s.namespace.name`, `k8s.container.name` | Carries UID natively — the *easy* dialect; included to keep maps plural | Resource-attribute vs metric-label placement varies by collector config |

**The honeypot, explicitly:** cAdvisor series must join via `(namespace, pod, container)` → UID, resolved against the pod informer / KSM mapping at ingest. A pod deleted and recreated **with the same name** is the race this invites; the lifecycle state machine's succession records (03 M3) plus creation-timestamp disambiguation close it. This exact scenario is a mandatory fixture in the Phase 0a join-accuracy gate.

### 3.3 Workload — Online Boutique first, OTel Demo as the corpus engine

**Phase 0 default: Google's Online Boutique** (microservices-demo): ~11 services, gRPC, a built-in Locust load generator, light enough for kind, and its Kubernetes manifests ship with resource requests/limits — which matters here, because config-sourced limits are what make bars resolvable (04). Verify limits per service at install and **deliberately customize**: tune one or two services' memory limits close to observed working set (to create honest near-threshold conditions), and strip limits from one deliberately, so the **resolvability-hole policy** (unbounded → listed, Tier-B-ineligible, never silent) is exercised from day one rather than discovered in production.

**Phase 1 addition: the OpenTelemetry Demo ("Astronomy Shop")** — recommended not for size but because its flagd feature flags are a **labeled-failure generator**, which is precisely what the falsification corpus (11 M3) needs:

| Demo flag | Induced behaviour | Maps to blueprint corpus need |
|---|---|---|
| `recommendationServiceCacheFailure` | Memory leak via an exponentially growing cache (~1.4× growth on ~50% of requests) | The marquee **working-set → OOM precursor** trajectory, on demand, with a known start time |
| `adServiceHighCpu` | High CPU load in one service (docs explicitly suggest setting CPU limits to demonstrate throttling) | CPU saturation / **throttling and noisy-neighbour** phenomena |
| `paymentServiceFailure`, `cartServiceFailure`, `productCatalogFailure` | Deterministic request errors in named services | Error-rate members of request-path phenomena |
| `kafkaQueueProblems` | Queue overload and consumer lag | Backpressure/lag phenomena |

Flags toggle at runtime through flagd's UI/API, so every induced incident arrives **with its label and timestamps** — the corpus annotation comes free.

**Beyond app-level faults: Chaos Mesh** (CNCF incubating, actively maintained, deploys on kind) for infrastructure-level induced failures via CRDs — `StressChaos` (drive memory/CPU pressure → real OOM kills and evictions), `PodChaos` (kills/restarts), `NetworkChaos` (latency, loss, partition), `IOChaos`. Chaos experiments are YAML CRDs, hence versionable next to the corpus they generate.

**Considered and set aside:** Sock Shop (long unmaintained), DeathStarBench and train-ticket (research-grade and heavyweight — revisit only if Phase 1 needs scale the demos cannot supply).

## 4. Ambiguity & incompleteness audit

Items the blueprint leaves silent or under-specified from an implementer's seat, beyond the existing open-question ledgers. Each carries a recommended initial answer so none blocks a first commit.

| # | Gap | Why it matters | Decide by | Recommended initial answer |
|---|---|---|---|---|
| A1 | **Scrape ownership** — does the system scrape exporters itself or sit behind a customer's Prometheus? | CEI stamping at ingest (03) assumes we own ingest; remote-read changes the whole identity story | Phase 0a, before 05 M1 | **Own the scraper** (parse exposition format directly from KSM/kubelet/node-exporter). A remote-write *receiver* mode is a later enterprise lane, never remote-read |
| A2 | **Deployment topology of the product itself** — in-cluster vs external is never stated | RBAC, kubelet-metrics reachability, packaging | Phase 0b → 1 | Dev: out-of-cluster binary via kubeconfig + API-server proxy for kubelet metrics. Phase 1: in-cluster Deployment with a read-only ClusterRole (pods, nodes, namespaces, services, endpointslices, PVCs, workloads, leases, events; `nodes/proxy` for cAdvisor) |
| A3 | **Evaluation tick and late-sample watermark** — cadence of fingerprint evaluation vs scrape, and how late a sample may arrive and still count | Determinism and replay equivalence depend on a defined watermark | Phase 0b, 05 M2 | Evaluation tick 15 s aligned to scrape; **watermark 30 s** (samples later than that join history and tombstoned identities but never retro-change an emitted fingerprint) |
| A4 | **Bar re-resolution latency** — operator edits a limit; how fast must the bar refresh? | A stale bar mis-grades every threshold state | Phase 0b, 04 M4 | Watch-driven: re-resolve affected bars **≤ 30 s** after the config event; `resolved-at` already surfaces staleness |
| A5 | **Rate-primitive constants** — window, smoothing, counter-reset rule are named but not numbered | 05 M2 cannot be coded without them | Phase 0b | Rate window **5 min**, smoothed first difference; counter reset = any decrease ⇒ restart segment, never extrapolate across; scrape gaps > 2 intervals break the window |
| A6 | **"Well-above" ladder constant** (05 §3.4 open question) | Loudness (08) keys off `well-above` | Phase 0b | Global initial convention: where bar = factor × limit (factor < 1), `well-above` = crossing the **raw limit itself**; otherwise 1.10 × bar. Calibrate in 11 M4 |
| A7 | **Findings persistence** — timeline (10) needs queryable history; blueprint names no store | Surfacing M2/M4 need it | Phase 0b | **SQLite** (pure-Go driver) for findings, selection records, unexplained cards, audit trails — zero-ops, replayable, fits the single-binary agent model |
| A8 | **Bound-customer-graph persistence** | Restart must not require re-discovery | Phase 0b | In-memory adjacency + periodic snapshot file + reload-then-reconcile on start. No graph database — thousands of nodes, 1–2-hop walks |
| A9 | **Cluster identity & multi-cluster** — CEI carries a cluster coordinate; source undefined | CEI minting needs it day one | Phase 0a | Cluster ID = `kube-system` namespace UID. Single-cluster scope pinned through Phase 2; multi-cluster is explicitly out of scope until then |
| A10 | **Role key for ownerless (bare) pods** | Role derivation (03 §3.1) assumes an ownership chain | Phase 0a | Fallback role key = `(namespace, kind=Pod, name)` flagged `bare`; succession by name+creation-timestamp |
| A11 | **Endpoints vs EndpointSlice** | The legacy Endpoints API is deprecated upstream | Phase 0a | **EndpointSlice only**, from the first informer |
| A12 | **Time semantics** | Validity intersection (03/07) breaks under skew | Phase 0a | All stamps wall-clock UTC at ingest receive time; event time recorded alongside when sources carry it; single-binary deployment makes cross-process skew a non-issue until the clock service splits out — then NTP-disciplined hosts + receive-time precedence |
| A13 | **Clock-service degradation surfacing** | Non-gating is designed (09); *visibility* of degradation is not | Phase 2 | Tier-B health panel: clock unreachable ⇒ early-warning surface shows "forecasting degraded since T", candidates age out, detection untouched |
| A14 | **The 842-node reference graph conversion** — exists as a design dataset, not yet schema-v1 YAML; effort unowned | 02 M2 is on the critical path of Phase 0a–0b | Phase 0a, assign now | Treat as a named workstream: schema v1 → mechanical conversion → lint pass → human review of every phenomenon's span/edge declarations |
| A15 | **TimesFM packaging confusion** | The PyPI package version (2.x) differs from the checkpoint version (2.5); easy to mis-pin | Phase 2, 09 M1 | Pin **both**: the `timesfm` PyPI/Git package version *and* the Hugging Face checkpoint `google/timesfm-2.5-200m-pytorch` by revision hash. CPU inference is sufficient for dev; GPU optional |
| A16 | **Replay-bundle data hygiene** | Bundles will eventually carry customer-shaped data | Phase 1 ledger (#6 adjacent) | Phase 0–1 corpora come only from our own clusters; anonymization standards land before any customer capture |

## 5. Constants adopted now (the parameters file, v0)

Single versioned file; every value below is revisable by harness evidence and carries its ledger/owner reference: scrape interval 15 s (dev) · reconciliation 30 s (dev) / 5 min (prod) · edge budgets per §1.2 · retracted-edge memory horizon 30 min · tombstones 15 min full / 24 h stub / 250 k cap · evaluation tick 15 s · watermark 30 s · rate window 5 min · default co-occurrence window 10 min (per-phenomenon override) · `well-above` per A6 · hot ring 60 min · warm retention 7 d · segment 2 h · fsync batch 1 s · bar re-resolution ≤ 30 s · Tier-B budget initial 50 clock invocations/cycle (dev).

---

*Companion document: `techstack.md` defines every technology these decisions assume.*
