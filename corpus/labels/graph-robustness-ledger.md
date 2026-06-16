# Graph-robustness #2 — wireability ledger (G0)

Captured live from `kind-vigil` (Online Boutique, ontology **v0.4.0**,
graphVersion `sha256:5a3b…59ed`, 34 entities) on **2026-06-16** via
`/api/coverage` + the KG `participates_in` membership + the authored
`detect-conditions-v*` overlays. This is the locked backlog for #2.

## The two ORTHOGONAL axes (the correction that reframes #2)

`/api/coverage` `observability` is an **obtainability** verdict (can the
phenomenon's required members be obtained on this cluster given
capabilities/tools/distro gates) — it is **NOT** "a detection check exists" and
**NOT** "a live fingerprint is firing". A phenomenon can be `full` observable and
still never produce a finding, because no `MemberCheck` binds a member to a
fingerprint facet.

| Axis | Source | Count |
|---|---|---|
| **Observable** (obtainable members) | `binding.PhenomenonObservability` → `/api/coverage` | **11 full · 12 partial · 17 none** (of 40 nodes; 38 real phenomena + 2 meta) |
| **Wired for detection** (has a `MemberCheck` → matcher can fire) | `detect-conditions-v1/v2/v3.yaml` | **7** |
| **Actually firing live now** | `/api/findings` | **1** (MEMORY_LEAK on currencyservice) |

**The 7 wired** (only these have a member→fingerprint check, so only these can
ever surface a `detect.Finding`):
`MEMORY_LEAK`, `THROTTLING_CASCADE`, `EVICTION_MEMORY`, `CONNTRACK_EXHAUSTION`,
`OOM_KILL_SYSTEM`, `STORAGE_SATURATION`, `OOM_KILL_CGROUP`.

## The LIVE firing universe (what a check can actually bind today)

The matcher (`detect/match.go satisfies`) is satisfied only against a
**materialized fingerprint variable** (`fp.Thresholds` / `fp.Rates`). The bound
threshold rules with `inst>0` (from `/api/coverage` `rules`) are the live
fingerprint universe:

- **container** (cAdvisor): `THR_CONTAINER_CPU_THROTTLE_RATIO` (21), `THR_CONTAINER_CPU_USAGE_VS_LIMIT` (31), `THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT` (31), `THR_CONTAINER_OOM_EVENTS` (31, but `container_oom_events_total=0` on kind → never breaches).
- **node** (node-exporter): `THR_NODE_CONNTRACK_UTILIZATION` (3), `THR_NODE_CPU_PSI_STALL` (3), `THR_NODE_IO_PSI_STALL` (3), `THR_NODE_FS_AVAIL_RATIO` (3), `THR_NODE_MEMAVAILABLE_VS_ALLOCATABLE` (3), `THR_NODE_OOM_KILL_BURST` (3).
- **out of scope** (no live series): `THR_CONTAINER_RESTARTS_RATE` (oos=31 — KSM `kube_pod_container_status_restarts_total` not scraped), `THR_PVC_USED_VS_REQUESTED` (0 — no declared-request PVC).
- **discrete events** (v3 T-C, NOT a fingerprint): pod-status `OOMKilled` / `CrashLoopBackOff` — visible as standalone `EventFinding`s, but **not yet a phenomenon-level finding**.

## The cascade map — EVERY authored cascade needs a NON-fingerprint lane

The 12 `phenomenon_relation` cascades, against what is wired (W):

| trigger | → | downstream | gap to light up live |
|---|---|---|---|
| **THROTTLING_CASCADE (W)** | → | **PROBE_FAILURE_RESTART** | downstream needs the **events lane** (CrashLoopBackOff) — *flagship* |
| **MEMORY_LEAK (W)** | → | **OOM_KILL_CGROUP (W)** | both wired, but downstream can't fire (`container_oom_events_total=0`) → needs the **events lane** (OOMKilled) — *flagship* |
| POD_STUCK_TERMINATING | → | VOLUME_MOUNT_FAILURE | both need events / KSM |
| CRI_ERRORS | → | PLEG_HANGS | both need the **kubelet-main** lane |
| RUNTIME_IO_STALL | → | PLEG_HANGS | both need the **kubelet-main** lane |
| PROBE_CASCADE_META | → | {THROTTLING_CASCADE (W), MEMORY_HIGH_THROTTLE, CONNTRACK_EXHAUSTION (W), KUBE_PROXY_SYNC_SLOW, ETCD_SLOW_PATH, NETWORK_IMPAIRMENT, PROBE_FAILURE_RESTART} | the 6-way root disambiguation; 2 roots already wired |

**Conclusion: no cascade completes off existing fingerprints alone.** The two
flagship cascades are exactly **one lane (events) away**. The events lane is the
highest-leverage #2 work and is already half-built (v3 T-C ingests the discrete
signals; they are surfaced standalone but never upgraded to a phenomenon match).

## G1 — LOCKED design: event-driven MEASURED phenomenon detection

Let a **real discrete event satisfy an authored required member**, producing a
**degraded `detect.Finding`** (MEASURED — the event is a fact; the finding is its
deterministic consequence), unioned with the fingerprint findings **at the
surface + cascade layer only**, kept **OFF the fingerprint replay-digest** (the
exact precedent set by the v2 cross-service warm path and v3 T-C corroboration —
a pure consequence of `findings`, never perturbing the tick or the digest). The
fingerprint detection replay guarantee is **untouched**; the event lane gets its
**own deterministic detection gate** with a label oracle (the v3 T-C pattern,
because events are not in the replay bundle).

Charter: event = MEASURED; match = MEASURED consequence; cascade = AUTHORED edge
lighting up; **join, never fuse**; degraded-honest (the gauge/probe members are
named unobservable). No learned anything.

**Targets (both real `required` members, referential-integrity holds):**
1. `PHEN_OOM_KILL_CGROUP` ← `SIG_oomkilled_oomkilling_d6c7e936` (OOMKilled). Lights up **MEMORY_LEAK → OOM_KILL_CGROUP** live (closes the `container_oom_events_total=0` blind spot).
2. `PHEN_PROBE_FAILURE_RESTART` ← `SIG_container_failure_events_backoff_crashloopbackoff_error_1cee58e3` (CrashLoopBackOff). Lights up **THROTTLING_CASCADE → PROBE_FAILURE_RESTART** live.

Governed overlay (a new `detections:` block in the event-conditions overlay,
referential to the phenomenon's member); golden fixtures (full producer path +
the no-false-upgrade negatives); a deterministic detection gate; live-verify with
real chaos pods (OOM recipe + a CrashLoop pod under CPU throttle).

## Honest backlog by lane (after G1)

- **G1 events-lane (this work):** OOM_KILL_CGROUP, PROBE_FAILURE_RESTART now; then IMAGE_PULL_FAILURE (ImagePullBackOff), INIT_CONTAINER_FAILURE (Init:CrashLoopBackOff), POD_STUCK_TERMINATING — same producer, more authored conditions.
- **G2 KSM lane (next, new generic scraper):** NODE_NOT_READY, EVICTION_MEMORY (upgrade), PDB_VIOLATION, VOLUME_MOUNT_FAILURE, HPA_FLAPPING, DISK_PID full, restart-count fingerprint (un-out-of-scopes THR_CONTAINER_RESTARTS_RATE).
- **G3 kubelet-main lane:** PLEG_HANGS, CRI_ERRORS, RUNTIME_IO_STALL, KUBELET_API_CONNECTION, IMAGE_GC, MEMORY_LEAK full, MEMORY_HIGH_THROTTLE → CRI/RUNTIME → PLEG cascades.
- **G4 author-only (cheap, off existing fingerprints):** DISK_PID degraded via `node_filesystem_files_free` (if materialized); ARP_NEIGHBOR via `node_arp_entries`.
- **Tier-4 DEFERRED (separate modality-expansion track):** control-plane deep (etcd/apiserver/scheduler/kube-proxy), CNI/DNS/mesh, GPU, logs/traces/L7. ~11-13 phenomena, new scrapers + RBAC + environment-dependent.

**Ceiling restated honestly:** #2 (G1-G4) reaches ~22-25 of 38 firing; Tier-4 is
the separate bigger track. Cluster-agnostic preserved (every condition binds a
signal pattern + a config-relative/flagged bar, never a service name).
