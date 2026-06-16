# event-detection gate — evidence (graph-robustness #2, G1)

**Verdict: PASSED** (`just event-detection-gate`, exit 0) — 2026-06-16.

## What it certifies

A discrete Kubernetes event that the KG authors as a **required member** of a
phenomenon produces a **DEGRADED, MEASURED** phenomenon finding, and — joined with a
fingerprint trigger finding on the same workload — lights up the **AUTHORED cascade**.
This wires two previously-dark flagship cascades:

- `MEMORY_LEAK → OOM_KILL_CGROUP` (OOMKilled event closes the `container_oom_events_total=0` blind spot)
- `THROTTLING_CASCADE → PROBE_FAILURE_RESTART` (CrashLoopBackOff event)

The event findings + augmented cascades ride **OFF the fingerprint replay digest** —
the deterministic detection guarantee is untouched (the v3 T-C / v2 cross-service
warm-path precedent). This is a **STANDALONE deterministic gate** (events are not in
the replay bundle): each scenario folds synthetic-but-real-shaped events through the
**REAL** `eventdetect.Findings` + `detect.Matcher.Cascades` + `replay.Digest`, graded
against a **LABEL ORACLE** fixed by construction, independent of the producer.

## Floors (all green)

| Floor | Result |
|---|---|
| **DETECTION-FIDELITY** | OK — produced findings == oracle, exact (3 findings graded) |
| **NO-FALSE-UPGRADE == 0** | 0 — a role-unresolved event (role-less Node) and an unauthored reason produce ZERO findings |
| **CASCADE-RECOGNITION** | OK — both authored cascades recognized, the `why` surfaced verbatim ("Eventual outcome", "Probe cascade"), no phantom |
| **DEGRADED-HONEST** | OK — every event finding degraded, requiredMet=1 < total, the missing members NAMED, the satisfied member is a `k8s-event:` member |
| **DIGEST-INVARIANCE** | OK — `digestBefore == digestAfter` per scenario (event detection off the fp digest) |
| **CHARTER == 0** | 0 — no finding/cascade row restates a cause |

## Scenarios (corpus/event-detection/)

`oom-detection` · `leak-to-oom-cascade` · `throttle-to-probe-cascade` ·
`role-unresolved-no-upgrade` · `unrelated-reason` · `healthy-negative`.

## Machinery

- Producer: `obsd/internal/eventdetect/eventdetect.go` (`Findings`).
- Authored conditions: `ontology/graph/overlays/experimental/event-conditions-v1.yaml` (`event_detections:` block — referentially validated against the released graph at load; lane stays off otherwise). Promotion into the released overlays is a governed follow-up (mirroring the cross-service relation's Phase A→C).
- Regen: `REGEN_EVENTDETECT_CORPUS=1 go test ./obsd/internal/eventdetect -run RegenEventDetectCorpus`.
- Always-on Go guards: `TestEventDetectDoesNotPerturbDigest` (digest off-ness), `TestEventDetectCorpusFrozenConsistent` (frozen-corpus drift), producer unit tests.
- Scorer: `harness/src/harness/event_detection_gate.py`; regression tests `harness/tests/test_event_detection_gate.py` (8 tests — good corpus passes + one mutation per floor).
- Recipe: `just event-detection-gate` (Go -race + Python).

## LIVE-VERIFIED on kind-vigil (2026-06-16)

`obsd --events-enabled --incident-memory --db ...` against the boutique + two
Deployment-backed chaos workloads (a gradual leaker→OOM, a throttled crash-looper):

- **`MEMORY_LEAK → OOM_KILL_CGROUP`** recognized on REAL **currencyservice** (MEASURED leak gauge + MEASURED OOMKilled event-driven finding, joined by the authored relation `why="Eventual outcome"`, related=same-entity), AND on the leaky chaos workload.
- **`THROTTLING_CASCADE → PROBE_FAILURE_RESTART`** recognized on the throttled crash-looper (`why="Probe cascade"`, related=same-entity).
- Event-driven `OOM_KILL_CGROUP` / `PROBE_FAILURE_RESTART` findings surface in `/api/insights`, degraded (1/8, 1/11), role-resolved, with the AUTHORED member note adjacent and the other required members named unobservable.
- **No-false-upgrade verified live**: bare Pods (no resolvable Deployment role) correctly produce NO phenomenon finding — only role-resolved workloads do.
- Non-gating: `--events-enabled` off ⇒ obsd byte-identical (full `go test -race ./obsd/...`); the event lane never enters the fp digest.

This is the blind spot closed end-to-end: the OOM and the probe-failure→restart loop —
invisible to the gauge path on kind — are now MEASURED findings that complete authored
causal stories with full provenance.
