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
