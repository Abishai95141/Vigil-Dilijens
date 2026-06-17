# ksm-gate — KSM telemetry lane (G2) evidence

**Gate:** `just ksm-gate` — PASSED. **Released:** v0.5.0
(`sha256:3cae50c4c6a92d2097ce3acccec34de011027e834478b172f4367c48eb65bb5c`).
**Class:** BEHAVIOURAL-MEDIUM (`just govern-classify v0.4.0 v0.5.0` →
"added DetectionCheck on PHEN_PROBE_FAILURE_RESTART").

## What G2 wires
The kube-state-metrics (KSM) object-state lane — graph-robustness #2 G2, the big
coverage unlock. KSM rides the **in-digest** fingerprint path like cAdvisor/node-exporter
(not the off-digest events lane), behind `--ksm-enabled` (default off ⇒ byte-identical).
The lead phenomenon: **PHEN_PROBE_FAILURE_RESTART**, whose restart-counter member
detect-conditions-v3 deliberately left unchecked ("the KSM-side restart/lastState
members have no scrapable channel here") — G2 ingests that channel and binds the check.

## Pipeline (the in-digest path)
`FetchKSM` (pods/proxy, Family=FamilyKSM) → `normalize.ksm()` (label-based identity:
`kube_pod_container_*`→container CEI, `kube_node_*`→node CEI, `kube_pod_*`→pod CEI,
`kube_deployment_*`→role CEI, PDB/replicaset/services→quarantine) → `streamSubID`
(KSM multi-dimensional families disambiguate by label, no mis-join) → the SAME
binding→materialize→matcher path as cAdvisor. Bar = the already-released
`THR_CONTAINER_RESTARTS_RATE` (>3 restarts/15m, FLAGGED default — borrowed-normativity
clean). Check = `detect-conditions-v4.yaml` (released).

## Floors (Go golden tests, -race, deterministic)
- **ingest-fidelity** — restart counter → 1 container stream; `kube_node_status_condition`
  (condition×status) → 3 DISTINCT streams (no mis-join); PDB object metric quarantines.
  (`observe.TestScrapeKSMResolvesByLabelIdentity`)
- **detection-fidelity** — a crossed restart-rate guard fires PHEN_PROBE_FAILURE_RESTART
  DEGRADED, 1-of-11 required members, the bar FLAGGED, unobserved members NAMED.
  (`detect.TestProbeFailureRestartFiresOnRestartRate`)
- **silence** — under the guard / no KSM variable ⇒ no fabricated alarm.
- **cascade-recognition** — a CPU-throttled crash-looper lights the AUTHORED
  THROTTLING_CASCADE → PROBE_FAILURE_RESTART relation ("Probe cascade" verbatim);
  topologically-unrelated entities NEVER pair.
  (`detect.TestThrottleToProbeRestartCascade`, `…UnrelatedNoCascade`)
- **non-gating** — the released v4 check is INERT without the KSM scrape (no restart
  stream ⇒ no finding ⇒ byte-identical detection). (`detect.TestKSMCheckReleasedButInertWithoutScrape`)

## Replay-diff (behavioural-medium evidence) — BOUNDED
The off-digest event-detection corpus drift is ONLY the `GraphVersion` stamp
(5a3b…59ed → 3cae…bb5c) — every finding/cascade byte-identical, proving the in-digest
v4 check does NOT perturb the independent off-digest eventdetect lane. The in-digest
replay fixture regenerated against the new pin. `event-detection-gate` re-grades the
regenerated corpus → PASSED.

## Live-verified on kind-vigil (all lanes, release v0.5.0)
- KSM deployed (`deploy/workloads/kube-state-metrics.yaml`), scraped (~10.8k series).
- Coverage 11→12 fully-observable; the silence-ledger "no emitting tool:
  kube-state-metrics" rows cleared (WATCHED, not no-stream-key).
- PHEN_PROBE_FAILURE_RESTART fires DEGRADED (completeness 0.091) on a real crash-looper.
- The flagship cascade lit up end-to-end:
  `THROTTLING_CASCADE → PROBE_FAILURE_RESTART ("Probe cascade")` on a CPU-throttled
  crash-looper (a previously-DARK cascade, now recognized live).

## G2b — label-select derivation + EVICTION_MEMORY (released v0.6.0, sha256:6d8f88be...)
The reusable keystone for multi-series KSM gauges: `observe.deriveKSM` re-emits the active
labelled row of a multi-dimensional gauge under a clean single-series metric (the matcher
needs one series per (entity,metric)). `kube_pod_status_evicted` is the Evicted-row
projection of `kube_pod_status_reason{reason}`, emitted only while a pod IS Evicted — a
deterministic projection (the KSM dialect, NOT learned); the bar stays authored.
PHEN_EVICTION_MEMORY (first-order, Node anchor) upgraded degraded->fuller via threshold-rules-v4
(THR_POD_EVICTED, structural flagged default) + detect-conditions-v5 (the evicted-pod
neighbour check, one runs-on hop). `just govern-classify v0.5.0 v0.6.0` = behavioural-medium.
Floors added (Go golden, -race): derivation fidelity (Evicted row -> single stream; inactive
rows not derived); EVICTION fuller (RequiredMet>=2 with the evicted member); validity contract
(no runs-on edge => the evicted member is NOT fabricated). FIRING is golden-certified only —
a real memory eviction is OOM-risky, NOT triggered live. v0.6.0 loads clean live.

## Honest scope — what is NOT wired
- PROBE_FAILURE_RESTART is the clean lead (single-series counter, no new machinery);
  EVICTION_MEMORY (G2b) is the first multi-series gauge wired via the derivation.
- **NODE_NOT_READY** is NOT cleanly KSM-wireable: its base KG members are node EVENTS +
  noise (node_lifecycle_events, node_netstat, hubble), not the `kube_node_status_condition`
  Ready gauge — it needs a NEW authored member (a bigger governance change) or the EVENTS
  lane (the NodeNotReady event). Stated, not forced.
- **PDB_VIOLATION** deferred: `kube_poddisruptionbudget_*` has no CEI mapping (quarantined)
  and PHEN_PDB_VIOLATION has zero base KG members — identity-layer work + an authored member.
- Next derivations (cheap, on this keystone): `kube_pod_status_phase{phase=Pending}` ->
  SCHEDULING_FAILURE; `kube_pod_container_status_last_terminated_reason{reason=OOMKilled}`.

## Batch 3 — DISK_PID_INODE_PRESSURE (v0.7.0, branch v4)
Wires the node-anchor member of PHEN_DISK_PID_INODE_PRESSURE (a CRITICAL operator blind
spot: a node shedding pods because nodefs / imagefs / inodes ran out). The member is the
kubelet's OWN `DiskPressure` node condition, fed by a NEW label-select derivation:
`kube_node_status_disk_pressure` = the `{condition="DiskPressure",status="true"}` row of
`kube_node_status_condition`, emitted ONLY while the kubelet reports the condition.

WHY the node condition, not node-exporter `node_filesystem_avail_bytes`: node-exporter
reports avail PER (device, mountpoint, fstype) — overlay, tmpfs, shm, root, image fs — so a
raw-ratio crossing on the WRONG mount is a false positive and the wrong one healthy is a
false negative. `DiskPressure` is the kubelet's SINGLE authoritative verdict over exactly
the filesystems it evicts on, evaluated against ITS configured thresholds: cluster-agnostic
(every kubelet computes it) and free of mountpoint ambiguity. Borrowed normativity at its
cleanest — the kubelet's number, never Vigil's, and never a guessed ratio.

Overlays: threshold-rules-v5 (THR_NODE_DISK_PRESSURE, absolute / above 0 / Node — structural
flagged default) + detect-conditions-v6 (the node-condition anchor check + the Node anchor
declaration). `govern-classify v0.6.0 v0.7.0` = behavioural-medium (new flagged rule + new
check on an EXISTING required member; no new phenomenon, no edited default). Frozen-corpus
drift on regen = the GraphVersion stamp ONLY (10 lines, all sha256/graph_version), proving
detection is byte-identical (the in-digest check is INERT without a disk-pressure stream).

Go golden floors (-race, in `just ksm-gate`): derivation fidelity (the DiskPressure=true row
-> ONE clean single-series stream value 1; the healthy status="false" row is NOT derived, so
a check never fires on a healthy node); FIRING (a node's disk-pressure variable produces a
DEGRADED DISK_PID_INODE_PRESSURE, the bar FLAGGED, the unobserved members NAMED); SILENT (a
healthy node -> no finding, also the non-gating assertion).

LIVE (v0.7.0, kind-vigil): obsd loads release v0.7.0 (14 rules, 13 overlays); the KSM lane
scrapes `kube_node_status_condition` every cycle (38 condition rows, ksm:1); THR_NODE_DISK_PRESSURE
binds to ALL 3 nodes (instantiated 3 / defaultBound 3). All 3 nodes report DiskPressure=false,
so the derived gauge has 0 streams and the bar is WATCHED-awaiting (zero false positives — the
kubelet's verdict is "no pressure"). A positive FIRING is golden-certified only: tripping real
kubelet disk-pressure needs filling the shared ~405 GB Docker-VM disk to nodefs<10%, unsafe on
this cluster (the documented env constraint) — NOT triggered live.

PID pressure shares this member signal and is a NAMED co-member (a follow-up once the
derivation strips selector labels so disk|pid can share one derived stream).

## Batch 4 — VOLUME_MOUNT_FAILURE (PVC stuck Pending) + the PVC dark-bar fix (v0.8.0, branch v4)
Closes the documented PVC "dark bar" AND wires a CRITICAL, LIVE-VERIFIABLE PVC phenomenon. A
PersistentVolumeClaim that cannot bind (a missing/misnamed storage class, no matching PV, a
zonal mismatch) sits Pending, and every pod that mounts it is stuck unschedulable — previously
invisible to Vigil.

THE IDENTITY FIX (code, not graph): the PVC is now a FIRST-CLASS identity instance.
- identity: the Watcher Observe()s PersistentVolumeClaims into the lifecycle store (upsertPVC,
  mirroring upsertNode); Lookup gains PVCUID (Store.lookup over the PVC kind); normalize routes
  kube_persistentvolumeclaim_* to the REAL PVC instance CEI (it was quarantined "unmapped");
  the binding compiler reads PVCs from the IDENTITY INVENTORY like pods/nodes and keys bindPVC
  on the real CEI (was a "pvc|ns|name" pseudo-key → StreamUID()="" → dark). This closes the gap
  binding.go itself flagged ("join identity once 03 tracks PVC lifecycles").

THE DETECTION (governed v0.8.0): threshold-rules-v6 (THR_PVC_PENDING, structural flagged
default) + detect-conditions-v7 (the PVC-anchor check on PHEN_VOLUME_MOUNT_FAILURE + the PVC
anchor). Fed by a NEW derivation: kube_persistentvolumeclaim_pending = the {phase="Pending"} row
of kube_persistentvolumeclaim_status_phase (emitted only while the claim is unbound). Chosen
over PVC FILL (THR_PVC_USED_VS_REQUESTED) deliberately: fill carries CAP_CSI_DRIVER and is
correctly gated OUT-OF-SCOPE on a CSI-less cluster (local-path), whereas the phase gauge is
emitted by KSM everywhere. `govern-classify v0.7.0 v0.8.0` = behavioural-medium.

Go golden floors (-race, in `just ksm-gate`): normalize PVC resolution (resolved / missing-label
/ unknown-pvc); derivation fidelity (Pending row -> one stream value 1; Bound/Lost NOT derived);
FIRING (a Pending PVC -> DEGRADED VOLUME_MOUNT_FAILURE, the bar FLAGGED, unobserved members
NAMED); SILENT (a Bound PVC -> no finding / non-gating); and the DARK-BAR-CLOSED silence test
(the PVC pair is WATCHED with a real CEI, no PVC no-stream-key silence remains). Frozen-corpus
regen drift = the GraphVersion stamp ONLY.

LIVE (v0.8.0, kind-vigil) — FIRING CONFIRMED (unlike DISK, this trip is safe to induce): a PVC
with a nonexistent storageClass (chaos/stuck-pending, zero resources) stays Pending; obsd loads
v0.8.0 (15 rules, 15 overlays); THR_PVC_PENDING binds 2 PVCs as first-class entities; the KSM
phase gauge routes to the real PVC CEI (i|<cluster>|chaos|PersistentVolumeClaim|stuck-pending|<uid>);
the derivation emits kube_persistentvolumeclaim_pending=1; and /api/findings shows
PHEN_VOLUME_MOUNT_FAILURE firing on it — "MEASURED match · AUTHORED pattern · graph 99aa7ab3",
honestly DEGRADED (the mounting-pod neighbour member NAMED unobservable — no pod mounts the orphan
claim). PVC fill (CSI volume-stats) remains the honest CSI-cluster follow-up (local-path emits
zero kubelet_volume_stats — re-confirmed this session).
