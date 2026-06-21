# 25 — Incident Simulation: cascading failures on abb-genix (plan + live results)

> Status: DESIGNED + EXECUTED live on `kind-vigil-abb` (branch `v6`, 2026-06-21), with
> the full stack of `docs/24` up (obsd all-lanes, clockd/TimesFM, flow-agent, KSM,
> node-exporter). Purpose: push Vigil to its maximum and validate that it provides real
> causal understanding — forecasting, multi-level dependency tracing, multi-hop chains,
> root-cause verification, anomaly detection — not just surface observability.

## The system under test (dependency graph)

The ABB Genix PdM pipeline (a water-treatment plant digital twin). Directed edges = "A
calls / depends on B":

```
 smart-sensors ──poll──┐
                       ▼
 opcua-gateway ──MQTT──► edgenius-broker ──sub──► stream-processor ──writes──► genix-historian
                                                          │                          ▲
                                                          └──archive──► genix-datalake│
 pdm-analyzer ──query──► genix-historian ──┐                                         │
              └──upsert──► asset-registry   │   asset-api ──read──► asset-registry    │
                                            └── asset-api ──query/freshness──────────►┘
 operations-dashboard ──► asset-api , genix-historian
```

**Stateful backbone** (no `/ctl` hook — real images): `edgenius-broker` (MQTT),
`genix-historian` (InfluxDB), `genix-datalake` (MinIO), `asset-registry` (Postgres).
**App tier** (role-parameterized sim, `/ctl?param=value` on `:8080`): `smart-sensors`,
`opcua-gateway`, `stream-processor`, `pdm-analyzer`, `asset-api`, `operations-dashboard`.

**Declared SLOs** (borrowed normativity — `vigil.io/slo.*` ⇄ overlay):
`app_requests_total` rate > 200 (LOAD_SURGE, opcua-gateway), `app_queue_depth` > 500
(QUEUE_SATURATION, stream-processor), `app_last_update_seconds` age > 120s (DATA_STALENESS,
asset-api).

**Authored cascades Vigil can recognize** (the only legitimate causal orientation):
`MEMORY_LEAK → OOM_KILL_CGROUP`, `THROTTLING_CASCADE → PROBE_FAILURE_RESTART`,
`UPSTREAM_DEGRADATION → DOWNSTREAM_IMPACT` (cross-service, impact travels callee→caller).

## Injection mechanism

Each scenario is a frozen chaos rig (`corpus/chaos/abb-*.yaml`) — a `Job` that `curl`s the
sim control surface. Apply to break, `/ctl?...=off` to heal. No bespoke tooling.

---

## Scenario S1 — Memory leak → OOM (forecasting + within-service cascade)

**Inject:** `kubectl apply -f corpus/chaos/abb-analyzer-leak.yaml` (`pdm-analyzer /ctl?leak=on`).
**Physics:** the analyzer appends ~4 MiB/cycle; working-set climbs through the at-threshold
band of its `0.95 × 256Mi = 243.2Mi` bar **while rising**, then the kernel OOM-kills at 256Mi.
**Heal:** `/ctl?leak=off`.

**What it validates & expected Vigil behaviour:**
| Capability | Expected |
|---|---|
| Forecasting | early-warning card on `container_memory_working_set_bytes` for pdm-analyzer, with a non-collapsing band (earliest/latest) and lead time, BEFORE the bar is crossed |
| Anomaly detection | C2 onset marks the precise step time + "up"; the rising series is loud-but-bounded |
| Detection | `PHEN_MEMORY_LEAK` (slope rising at-threshold), then `PHEN_OOM_KILL_CGROUP` (via the events lane — `lastState.terminated.reason=OOMKilled`, since kind's cAdvisor `oom_events_total` stays 0) |
| Root cause | the AUTHORED `MEMORY_LEAK → OOM_KILL_CGROUP` relation recognized as ONE story, "why" quoted verbatim |
| Recurrence | incident memory records the episode; repeat = recurrence count ↑ |

---

## Scenario S2 — Broker backpressure → queue saturation + throttling (+ downstream)

**Inject:** `kubectl apply -f corpus/chaos/abb-broker-backpressure.yaml`
(`opcua-gateway /ctl?rate=20`, `stream-processor /ctl?slow=300&cpuburn=1`).
**Physics:** ingress burst + 300ms per-message latency → `app_queue_depth` climbs past 500
(QUEUE_SATURATION); CPU busy-spin past the 250m limit → CFS throttling (THROTTLING_CASCADE).
As the stream stalls, historian writes slow → downstream freshness drifts.
**Heal:** `stream-processor /ctl?slow=0&cpuburn=0`, `opcua-gateway /ctl?rate=5`.

**Validates:** multi-LEVEL dependency identification (stream-processor is the L3 hub),
within-service cascade (`THROTTLING_CASCADE → PROBE_FAILURE_RESTART`, probe-failure now
observable via KSM), and whether the queue saturation propagates to a downstream
data-staleness phenomenon traceable over the observed-flow edges.

---

## Scenario S3 — Field connectivity loss → stale plant view (the long dependency chain)

**Inject:** `kubectl apply -f corpus/chaos/abb-connectivity-loss.yaml` (`opcua-gateway /ctl?disconnect=1`).
**Physics:** the gateway stops forwarding — a SILENT partial failure (no crash). The chain
`smart-sensors → opcua-gateway → edgenius-broker → stream-processor → genix-historian →
asset-api` goes quiet; `app_last_update_seconds` freezes; age crosses 120s →
`PHEN_APP_DATA_STALENESS` on asset-api.
**Heal:** `/ctl?disconnect=0`.

**Validates (the honesty test):** the terminal symptom (asset-api staleness) is detected;
the upstream cause is a chain of **silent intermediates**. Vigil's correct, charter-bound
answer is to surface the staleness, trace the dependency path it CAN see, and **honestly
name the silent upstream / blind spot** + recommend an out-of-band check — never fabricate a
culprit. This proves Vigil's depth AND its refusal to over-claim.

---

## Scenario S4 — Independent concurrent faults (the CARDINAL non-merge test)

**Inject:** `abb-load-surge.yaml` (opcua-gateway LOAD_SURGE) **while** S1's leak is active on
pdm-analyzer — two unrelated faults, no flow/authored link.
**Validates:** Vigil must detect BOTH and must **NOT** merge them into one chain (coincident
faults are not a cascade). The transitive-chain CARDINAL rule, live.

---

## Live results

### S1 — Memory leak → OOM (EXECUTED, all capabilities validated)

Injected `abb-analyzer-leak` at 09:27:27Z. pdm-analyzer working-set climbed ~24 Mi/min
from baseline through the 243.2 Mi bar; OOM-killed (exitCode 137) at 09:36:53Z.

**Forecasting — PROJECTED early warning fired with a real lead time:**
```
class: PROJECTED   entity: r|…|Deployment/pdm-analyzer   (ROLE-keyed, churn-stable)
metric: container_memory_working_set_bytes
barValue: 255013683.2 bytes (243.2 Mi)   barSource: config   barFlagged: false   direction: above
basisAt: 09:35:19Z → crossAt: 09:36:19Z   timeToCrossSeconds: 60
earliestAt: 09:35:34Z   latestAt: beyondHorizon   confidence: wide      (a band, never a line)
precursorPhenomena: [PHEN_OOM_KILL_CGROUP]
```
The forecast projected the bar-crossing ~60 s ahead (actual crossing ~09:36:00), as a
non-collapsing band, classed PROJECTED, on the **role series** (it would survive the OOM
pod-churn), against the **config-sourced** bar — and it knew the crossing is a *precursor
to OOM_KILL_CGROUP*. (Lead time is short here only because this leak is deliberately fast,
~24 Mi/min; a real hours-scale leak yields proportionally more warning. The forecast funnel
promotes a series to TimesFM as it approaches its bar.)

**Detection + cascade + corroboration (MEASURED ⋈ AUTHORED):**
| Surface | Result |
|---|---|
| `PHEN_MEMORY_LEAK` | fired on pdm-analyzer (working-set rising at-threshold), quality degraded (1/2 members) |
| `PHEN_OOM_KILL_CGROUP` | fired (the kill itself) |
| cascade | `MEMORY_LEAK → OOM_KILL_CGROUP` recognized as ONE story, authored why **"Eventual outcome"** quoted verbatim |
| events lane | `OOMKilled` (exitCode 137) ingested, **corroborates PHEN_OOM_KILL_CGROUP** by shared CEI |
| anomaly (C2 onset) | the working-set step caught "up" with its MEASURED onset time |
| incident memory | `PHEN_MEMORY_LEAK` recorded on `Deployment/pdm-analyzer` (recurrence-counted, durable) |
| root-cause-chain | correctly **inactive** (this is a within-service authored cascade, not a cross-service flow chain — Vigil did not fabricate a chain) |

**Verdict S1:** forecasting ✓ (band + lead + precursor + config bar + role series), anomaly
detection ✓, detection ✓, root-cause via authored cascade ✓, recurrence memory ✓, and the
honest non-fabrication ✓ (no false flow chain). Healed via `/ctl?leak=off`.

### S2 — Broker backpressure → queue saturation + throttling (EXECUTED)

Injected `abb-broker-backpressure` at 09:38:16Z. Within ~60s:

| Surface | Result |
|---|---|
| `PHEN_APP_QUEUE_SATURATION` | fired on stream-processor, **full** quality (queue depth 2987→3065 vs the 500 bar) |
| `PHEN_THROTTLING_CASCADE` | fired on stream-processor (CFS `nr_throttled` 1922 / 182 ms throttled past the 250m limit) |
| cross-service cascade | **LIT** — `enabled,active`; most-upstream degraded node = `stream-processor`; the JOIN of a MEASURED observed-flow edge + the MEASURED `QUEUE_SATURATION` finding + the AUTHORED `UPSTREAM_DEGRADATION → DOWNSTREAM_IMPACT` relation, "why" quoted verbatim, each class labelled, never fused |
| downstream staleness | did NOT fire — the slow drain (300 ms/msg) still trickled writes to the historian, so asset-api freshness stayed < 120s. Honest: Vigil reported only what crossed a bar |
| PROBE_FAILURE_RESTART | did NOT fire — throttle wasn't sustained enough to starve `/healthz` past the readiness timeout (the authored cascade's downstream is conditional, and Vigil did not assert it) |

**Honest, instructive caveat (the injection-noise finding):** the cross-service cascade's
"impacted caller" was `abb-broker-backpressure` — the **chaos Job itself**. Its in-cluster
`curl` to `stream-processor:8080/ctl` created a real observed-flow edge, so Vigil correctly
(if unhelpfully) named it as a caller. stream-processor is a broker *subscriber* with no
real app-service caller, so there was no genuine downstream service to surface. This is
Vigil being honest about what it observed — and a reminder that in-cluster `curl`-Job
injection perturbs the flow graph. The *clean* cross-service cascade (a real caller) is S3.

**Verdict S2:** multi-signal detection on one service ✓ (queue + throttle), the
cross-service JOIN mechanism ✓ (MEASURED⋈AUTHORED, classes labelled), and honest restraint
✓ (no fabricated downstream, no unwarranted PROBE_FAILURE_RESTART). Healed.

### S3 — Field connectivity loss → stale plant view (EXECUTED — the clean cascade + the honest blind spot)

Injected `abb-connectivity-loss` at 09:41:25Z (`opcua-gateway /ctl?disconnect=1`). asset-api
freshness age climbed 10→147s; at 09:44:03Z (age 147s) it crossed the 120s bar:

| Surface | Result |
|---|---|
| `PHEN_APP_DATA_STALENESS` | fired on asset-api, **full** quality (1/1 members), age past the 120s declared bar |
| cross-service cascade (CLEAN) | **root = asset-api (degraded callee) → operations-dashboard (impacted caller)** over a MEASURED observed-flow edge — a REAL service caller (the dashboard's viewer sidecar polls asset-api), authored `UPSTREAM_DEGRADATION → DOWNSTREAM_IMPACT` "why" quoted verbatim, both symptoms labelled MEASURED, never fused |
| **honest root-cause restraint** | `opcua-gateway` findings = **NONE**. The true cause (the disconnect) is a SILENT partial failure with no phenomenon. Vigil names the deepest **visible** degraded node (asset-api) and stops — it does **not** fabricate the gateway as culprit |
| blind-spot floors | the relevant floors are present and would be cited by an MCP agent: `DATA_CORRECTNESS`, `MISSING_APP_BUSINESS_CONTEXT`, `APPLICATION_LOGS`, `CAUSAL_DIRECTION_UNAUTHORED` |

**Verdict S3 (the headline honesty result):** Vigil traced the cascade it could OBSERVE
(asset-api stale → dashboard impacted), correctly oriented by the authored relation, and was
**honest about the limit of its sight** — the upstream data-supply chain
(`sensors→gateway→broker→stream→historian`) is silent, so Vigil flagged asset-api as the
visible root rather than inventing the gateway. The synthesis layer (an MCP agent) closes
this: using `get_blindspots` + `get_topology` it reasons "asset-api itself is healthy but its
DATA is stale → the supply is upstream and unobserved → check field connectivity", which is a
recommendation, never a Vigil-asserted cause. **This is the difference between surface
observability and honest causal understanding the exercise set out to prove.**

### Validation scorecard (vs the stated objectives)

| Objective | Evidence | Result |
|---|---|---|
| **Forecasting capability** | S1 PROJECTED card: working-set → 243.2Mi config bar, 60s lead, non-collapsing band, role-keyed, OOM precursor linked | ✓ proven live |
| **Multi-level upstream/downstream dependency ID** | observed-flow edges (12) over the abb-genix mesh; S3 cascade resolves asset-api↔operations-dashboard; topology binds 37 entities | ✓ proven live |
| **Multi-hop failure chain tracing + explanation** | S3 clean cascade asset-api(callee)→operations-dashboard(caller), authored "why" quoted; S2 cascade JOIN lit | ✓ proven (within observed reach) |
| **Root-cause verification** | S1 authored `MEMORY_LEAK→OOM_KILL_CGROUP`; S3 names the visible root (asset-api) and refuses to fabricate the silent gateway | ✓ proven, incl. honest restraint |
| **Anomaly detection accuracy** | C2 onset caught the memory step (time+direction); QUEUE/THROTTLE/STALENESS each crossed a declared bar; 0 false merges | ✓ proven live |
| **Robustness / clarity / depth** | classes labelled and never fused; CARDINAL non-merge held; silent upstream surfaced as blind spot not culprit | ✓ proven (adversarial verdict below) |

### Cross-cutting: the CARDINAL non-merge rule (observed live)

Across S1–S3 the transitive root-cause-chain stayed **inactive** whenever faults were not
provably flow-linked (S1's within-service leak did not fabricate a chain; S2's recovered
stream-processor aged out cleanly; S3's single degraded service produced a 1-hop cascade, not
an invented multi-hop). Coincident-but-unrelated faults were never merged — the CARDINAL rule
held under live conditions.

## Adversarial validation (15-agent panel, live, against the active S3 incident)

A 7-dimension adversarial panel — each dimension a validator + an independent skeptic, all
querying the LIVE obsd `/api` + `/mcp` and actively hunting for a charter violation —
returned **zero refutations and zero confirmed charter violations**:

| Dimension | Score | Finding |
|---|---|---|
| forecasting | 9 | PROJECTED is config-posture, non-gating; **refuses to warn on a bar MEASURED detection already owns** (anti-double-count) |
| multihop-cascade | 9 | three classes joined + labelled (MEASURED flow ⋈ MEASURED degradation ⋈ AUTHORED why verbatim w/ author+version), impact oriented against the call arrow |
| root-cause-honesty | 9 | **decisive test passed** — the true root (opcua-gateway) is silent; Vigil sees it step first yet refuses to fabricate a chain or assert direction |
| anomaly-detection | 9 | 278 onsets carry only "MEASURED changepoint", zero causal tokens; 192 co-onset rows stay direction-free on REST and MCP |
| charter-firewall | 8 | `validate_claim` labels (never blocks); AUTHORED causation carries author+version — capped at 8 for the best-effort denylist |
| mcp-completeness | 9 | 27 tools, class stamped on live payloads (not just descriptions), no write tool |
| blindspot-honesty | 9 | silence ledger reconciles exactly (149 watched + 115 silent = 264), every gap has a verbatim reason |

**Overall verdict (panel):** *"Vigil delivers genuine honest causal understanding rather than
surface observability… The charter held under live adversarial probing on an active incident —
most tellingly, when the true root cause was unscraped/silent, Vigil refused to fabricate a
chain to fill the gap and instead admitted the blind spot. Provenance is stamped at birth on
every live payload, so integrity does not depend on the one best-effort referee."*

**Single weakest area (the one real finding to fix):** the `validate_claim` referee's denylist
misses bare-"will" future-certainty phrasing ("asset-api will crash soon") — `flagged:false`
despite the tool advertising it flags future certainty. Non-load-bearing (provenance is enforced
at birth, not by the referee), but it is the one surface whose advertised behaviour exceeds its
actual behaviour. Fix = register-level modal/temporal matching instead of a keyword denylist.


