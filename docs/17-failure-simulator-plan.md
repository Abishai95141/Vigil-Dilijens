# 17 — Failure & Load Simulator Plan (Streamlit)

> **Status: PLAN (PROPOSED).** Nothing in this document is built yet. It is a
> design for a Streamlit-driven *demonstration harness* that induces failures,
> traffic, and cascades on a **target cluster** so that an operator can watch
> Vigil detect (MEASURED), forecast (PROJECTED), and surface authored cause
> (AUTHORED) in real time. Every trigger below is grounded in a real chaos rig
> under `corpus/chaos/` or a real route under `obsd/internal/api/`. Where a
> capability is gate-pending or environment-limited, it is labelled honestly.
>
> Owning context: the WIRED-phenomena list and the charter
> ([`docs/01-epistemic-separation-charter.md`](01-epistemic-separation-charter.md)),
> the capability architecture ([`docs/15-capability-architecture.md`](15-capability-architecture.md)),
> the cross-service flow lane ([`docs/15-cross-service-flow-lane.md`](15-cross-service-flow-lane.md)),
> and the environment caveats in
> [`docs/14-implementation-clarifications.md`](14-implementation-clarifications.md) §3.1.

---

## 1. Goal & showcase narrative

The simulator exists to make Vigil's epistemic discipline **visible on a live
cluster** within a few minutes, without a human hand-editing manifests at a
terminal. The narrative arc the operator should experience, every time:

1. **Induce.** The operator moves one control (a slider, a toggle). The
   simulator templates and applies a chaos manifest (grounded in
   `corpus/chaos/`), patches a Deployment, scales a load generator, or pokes a
   workload's control surface.
2. **Vigil DETECTS now (MEASURED).** Within a scrape window or two the
   deterministic path lights up: a finding on `/api/findings`, an insight card
   on `/api/insights`, a discrete event on `/api/events`. This is read from the
   store or is a deterministic arithmetic consequence of facts — never a guess.
3. **Vigil FORECASTS soon (PROJECTED).** For the slow-creep scenarios, TimesFM
   warns on `/api/warnings` that a series will cross its **borrowed** bar ahead
   of time, with an uncertainty band that **never collapses to a line**. The
   anticipatory cross-service cascade (`/api/cross-service`) can fire *minutes
   before* the MEASURED one (see §10, the lead-time story).
4. **The MCP-connected AI synthesizes cause & fix.** An MCP client (the relay
   chain over `/mcp`) reads the **classed** facts — the MEASURED finding, the
   AUTHORED trigger→downstream relation, the PROJECTED band — and an operator
   asks it "what is happening and what do I do?". The synthesis is the AI's, the
   grounding is Vigil's; the provenance labels survive into the answer.

The simulator is the *induction half* of that loop. Vigil is unchanged — the
simulator only drives the cluster and **reads back** Vigil's public `/api`. It
adds **no** detection logic, **no** thresholds, **no** learned anything. If the
simulator ever tuned a parameter based on what Vigil reported, it would launder
class across the surfacing boundary; it must not (see §11).

---

## 2. Architecture

Three layers, kept deliberately thin and cluster-agnostic:

```
┌──────────────────────────────────────────────────────────────────┐
│  Streamlit UI (operator-facing)                                    │
│    • the control surface (§3): sliders / toggles / buttons         │
│    • a live read-back panel per Vigil /api endpoint (§5)           │
│    • scenario launcher (§10) + safety state (dry-run, teardown)    │
└───────────────┬───────────────────────────────┬──────────────────┘
                │ drives                          │ polls
                ▼                                 ▼
┌──────────────────────────────┐   ┌──────────────────────────────────┐
│  Controller layer (Python)   │   │  Vigil read-back client          │
│   • manifest templating      │   │   • HTTP GET the /api surfaces     │
│     (Jinja over corpus/chaos)│   │   • POST /api/validate-claim       │
│   • kubectl apply/patch/scale│   │   • POST /mcp (relay tools)        │
│     via the cluster's API    │   │   • renders class-labelled cards   │
│   • a load generator (RPS)   │   │     (never fuses MEASURED/PROJECTED│
│   • workload /ctl poke        │   │      /AUTHORED — §6)               │
│   • SAFETY RAILS (§6) gate    │   └──────────────────────────────────┘
│     EVERY mutating action     │
└───────────────┬──────────────┘
                │ talks to
                ▼
        ┌───────────────────────────────────┐
        │  TARGET CLUSTER (parameterized)    │
        │   • a kubeconfig + context         │
        │   • a target namespace / services  │
        │   • Vigil (obsd) running its lanes │
        └───────────────────────────────────┘
```

Design rules:

- **The controller never hardcodes a service, namespace, or limit.** All of
  those are config (§9). The shipped chaos rigs (e.g. `corpus/chaos/leak-oom.yaml`
  pins `namespace: chaos` and `nodeSelector: vigil-worker`) are used as
  **templates** — the simulator overrides namespace, node selector, image,
  limits, and timings at apply time so the same rig runs against any cluster.
- **The controller is the only thing that mutates the cluster.** The read-back
  client is GET-only against `/api` (plus the explicitly-POST `/api/validate-claim`
  and the `/mcp` JSON-RPC endpoint). Vigil is never written to.
- **Every mutating action passes through the safety gate (§6) first.** Dry-run
  short-circuits the controller to "render the manifest / kubectl args, apply
  nothing".

---

## 3. The control surface

This is the heart of the simulator: a flat inventory of operator controls. One
row per control. Each control maps to a concrete cluster action grounded in a
real rig, the phenomenon it is *designed* to light up, and the Vigil surface
that reacts. (Phenomenon names are the WIRED set from
[`docs/00-master-system-design.md`](00-master-system-design.md); app-SLO bars are
the annotations in `corpus/chaos/app-signal-capA.yaml:24-26`.)

| Control | Knob / range | Cluster action (grounded) | Phenomenon it lights up | Vigil surface that reacts |
|---|---|---|---|---|
| **Memory-leak slider** | prefill `Mi` (e.g. 60–500) + leak rate `Mi/min` (e.g. 1–15) + limit `Mi` | Template `corpus/chaos/leak-oom.yaml` (fast prefill then `1Mi/10s` slow leak under a declared `limits.memory`; bar = `0.95×limit`, `leak-oom.yaml:43-60`); apply to target ns | `PHEN_MEMORY_LEAK` (MEASURED, degraded, rising through the 0.95×limit band) | `/api/findings`, `/api/insights` (leak card with at-risk blast radius) |
| **OOM cascade toggle** | on → let the leak reach `limit`; off → cap below bar (plateau) | Same rig but allow the kernel to OOM-kill the cgroup at `limit` (`leak-oom.yaml:4-11` doc comment) | `PHEN_OOM_KILL_CGROUP` after the leak; the authored `MEMORY_LEAK→OOM_KILL_CGROUP` relation recognized as one story | `/api/events` (the kill; `container_oom_events_total=0` on kind → seen via the events lane + `lastState.terminated`), `/api/insights` cascade card |
| **Creep-to-plateau toggle** | prefill `Mi` + creep `Mi/min`, **no kill** | Template `corpus/chaos/leak-creep-demo.yaml` (tmpfs `emptyDir medium:Memory`, prefill ~120Mi then ~15Mi/min creep to just above the bar, then plateau at `100000s` — `leak-creep-demo.yaml:1-58`) | `PHEN_MEMORY_LEAK` that **rises and holds** without an OOM — the safe forecast demo | `/api/findings` (firing-now while rising), `/api/warnings` (PROJECTED crossing ahead of the bar) |
| **CPU-throttle toggle** | victim CPU limit `m` (e.g. 100m) + pressure pods `N` (e.g. 6) | Template `corpus/chaos/cpu-cascade.yaml`: a `100m`-limited spinner victim that throttles >25% of periods, plus `cpu-hog × N` to push node PSI `/proc/pressure/cpu` past the `0.10` bar (`cpu-cascade.yaml:23-72`) | `PHEN_THROTTLING_CASCADE` (victim throttle + node CPU pressure) | `/api/findings`, `/api/insights` (throttle cascade); `/api/events` if it drives a probe restart |
| **IO / storage-pressure toggle** | replicas `N` (e.g. 2) + fsync chunk `Mi` | Template `corpus/chaos/io-pressure.yaml`: a `dd` fsync loop (8Mi chunks, 32-iter) to a node-backed `emptyDir`, driving `/proc/pressure/io` some-stall past `0.10` (`io-pressure.yaml:18-59`) | `PHEN_STORAGE_SATURATION` (DEGRADED, node anchor) | `/api/findings`, `/api/insights` |
| **Traffic / RPS slider** | target rate `req/s` (e.g. 0–200) against the app-signal pod | Controller load generator drives the workload; OR poke `app-signal-capA.yaml`'s `/ctl?rate=120` control surface (`app-signal-capA.yaml:78-79`) past the declared `slo.requests.max_rate=50` bar | `PHEN_APP_LOAD_SURGE` (rate > declared `vigil.io/slo.requests.max_rate`) | `/api/insights` (app-SLO card), `/api/findings` |
| **Queue-depth slider** | depth value (e.g. 0–400) | Poke `/ctl?queue=250` on the app-signal pod (`app-signal-capA.yaml:78-79`) past `slo.queue.max_depth=100` (`:24`) | `PHEN_APP_QUEUE_SATURATION` (queue_depth > declared bar) | `/api/insights`, `/api/findings` |
| **Data-staleness toggle** | freshness on/off | Poke `/ctl?frozen=1` to stop advancing `app_last_update_seconds`; `(evalNow − last_update)` crosses `slo.freshness.max_age=60` (`:25`) | `PHEN_APP_DATA_STALENESS` (the differentiator: age-from-timestamp) | `/api/insights`, `/api/findings` |
| **Probe-fail toggle** | latency / 5xx on the readiness path | Patch the target Deployment's `readinessProbe`/`livenessProbe` to point at a failing path (or set `/ctl` to fail health), forcing restart events | `PHEN_PROBE_FAILURE_RESTART` (restart counter via KSM lane) | `/api/events` (CrashLoopBackOff / restart), `/api/insights` cascade card |
| **Image-pull-fail toggle** | good image ↔ bad tag | Patch a Deployment's container `image:` to a nonexistent tag → `ImagePullBackOff`/`ErrImagePull` Event (events lane, `docs/15-capability-architecture.md` §8) | `PHEN_IMAGE_PULL_FAILURE` (MEASURED event-class) | `/api/events` (the discrete event), `/api/insights` |
| **PVC-pending toggle** | request a PVC on an unsatisfiable storageClass | Apply a `PersistentVolumeClaim` that cannot bind → PVC stuck `Pending` (observable via KSM; PVC **fill** is NOT observable on kind — no `kubelet_volume_stats`) | `PHEN_VOLUME_MOUNT_FAILURE` (PVC PENDING via KSM) | `/api/events`, `/api/findings` |
| **Eviction-pressure control** | node memory headroom target | Drive node memory pressure (bounded — see §6) so the kubelet evicts a low-priority pod | `PHEN_EVICTION_MEMORY` (eviction event) | `/api/events`, `/api/insights` |
| **Conntrack-pressure toggle** | concurrent connections `N` | Load generator opens many short-lived connections to push `nf_conntrack` usage toward its limit | `PHEN_CONNTRACK_EXHAUSTION` | `/api/findings`, `/api/insights` |
| **CASCADE trigger (transitive)** | tiers `N` (default 3) + prefill `Mi` + rate `Mi/min` + limit `Mi` | Template `corpus/chaos/chain3-rootcause.yaml`: N tiers (`chain-back ← chain-mid ← chain-front`), each prefilled ~250Mi + `6Mi/min` under a `320Mi` limit, each holding a socket to its callee for the flow edge (`chain3-rootcause.yaml:1-138`) | A MEASURED transitive chain (Cap B) oriented by the AUTHORED relation; coincident-but-unrelated faults never merge | `/api/root-cause-chain` (the ordered chain), `/api/cross-service` (the MEASURED fan), `/api/topology` (flow edges) |
| **CASCADE trigger (anticipatory forecast)** | prefill ~500Mi + `6Mi/min` ramp + limit (bar≈665Mi) | Template `corpus/chaos/flow-forecast-p1.yaml`: `leakx-callee` leaks smoothly while serving; `leakx-caller` holds a persistent TCP connection (the flow edge); TimesFM projects the crossing ~10min ahead (`flow-forecast-p1.yaml:1-46`) | The PROJECTED anticipatory cross-service cascade fires *before* the MEASURED one (the lead-time story, §10) | `/api/warnings` (PROJECTED, band), `/api/cross-service` (anticipatory chain), then `/api/cross-service` again when the MEASURED cascade confirms |
| **Fan-in trigger (multi-root)** | callers `N` (default 3) + hub limit `Mi` | Template `corpus/chaos/flow-fanin-demo.yaml`: `leaky-hub` (prefill 340Mi, `1Mi/10s`, 384Mi limit, bar≈365Mi) with `hub-caller-a/b/c` each holding a connection (`flow-fanin-demo.yaml:1-152`) | A 1→N fan-in cascade — exercises multi-root cross-service detection | `/api/cross-service`, `/api/topology` |

> Controls that drive **node-level** pressure (CPU PSI, IO PSI, eviction,
> conntrack) are realism-limited on kind because kind nodes share the host
> kernel (doc 14 §3.1). The simulator should label these controls
> "node-pressure realism limited on kind" in the UI rather than implying a
> faithful node-pressure falsification (see §6, §11).

---

## 4. Showcase scenarios

Each scenario is a scripted sequence of control moves the simulator can launch
with one button. They are **sequential** (one rig at a time — §6).

**S1 — "Detect now": the leak→OOM cascade.**
Move the *Memory-leak slider* (prefill 60Mi, 1Mi/10s, 96Mi limit) and enable
the *OOM cascade toggle*. Watch `/api/findings` light `PHEN_MEMORY_LEAK` while
rising; minutes later the kernel kills the cgroup and `/api/events` shows the
OOM (via the events lane on kind), and `/api/insights` recognizes the authored
`MEMORY_LEAK→OOM_KILL_CGROUP` story (`leak-oom.yaml:4-11`).

**S2 — "Forecast soon": the lead-time story (the headline).**
Launch the *anticipatory forecast cascade* (`flow-forecast-p1.yaml`: prefill
500Mi, +6Mi/min, bar≈665Mi). TimesFM warns on `/api/warnings` that the leaking
callee will cross its sub-bar, and the anticipatory cross-service cascade fires
on `/api/cross-service` **~10 minutes before** the MEASURED cascade confirms
(`docs/15-cross-service-flow-lane.md` §2). The band on the warning **widens with
horizon and never collapses to a line.** This is the demo that proves "now vs
soon" as two separate, separately-labelled classes.

**S3 — "Connect the chain": transitive root cause.**
Launch the *CASCADE trigger (transitive)* (`chain3-rootcause.yaml`, 3 tiers,
+6Mi/min, 320Mi limit). As each tier degrades, `/api/root-cause-chain` stitches
`back→mid→front` into one ordered chain over observed-flow edges, oriented only
by the authored relation. Demonstrate the cardinal rule by *also* starting an
unrelated single leak elsewhere — it stays a separate card, never merged.

**S4 — "App SLOs": queue / staleness / load.**
Against the app-signal workload, sweep the *Queue-depth slider* past 100, flip
the *Data-staleness toggle* (`/ctl?frozen=1`), and push the *Traffic slider*
past 50 req/s. Three distinct app-SLO phenomena appear on `/api/insights`, each
against a bar **borrowed from the pod's own annotations**
(`app-signal-capA.yaml:24-26`) — never a learned baseline.

**S5 — "The blind spot is honest".**
Toggle the *PVC-pending* control (binds → observable via KSM) and then *attempt*
a PVC **fill** (NOT observable on kind). The simulator shows that PVC PENDING
surfaces on `/api/events` while PVC fill is reported by Vigil as an honest
coverage gap on `/api/coverage` / `/api/silence-ledger` — never silently
"green". This scenario sells the *partial-coverage honesty* of the charter.

**S6 — "Ask the AI".**
After any scenario, the operator types a question into the MCP panel. The relay
reads the classed facts and the AI synthesizes cause + remediation, with the
MEASURED / PROJECTED / AUTHORED labels carried into the answer. `/api/validate-claim`
can then be POSTed the AI's own sentence to get the honest-labeler verdict.

---

## 5. The read-back loop

The Streamlit app polls Vigil's public surfaces on a fixed interval (e.g. every
scrape window) and renders one panel per endpoint. All routes are mounted by
`Register` in `obsd/internal/api/server.go:87-360`. Each carries an **honest
unavailable / OFF** state when its lane is not running — the panel must render
that verbatim note, never an empty "all clear".

| Endpoint | What the panel shows | Notes (grounded) |
|---|---|---|
| `GET /api/coverage` | observability vector: which (entity,variable) pairs are watched vs dark | honest "not-yet-compiled" when binding hasn't run |
| `GET /api/findings` | the firing/last-seen findings table | up to **200** rows; each marked stale vs firing-now at serve time via `MarkFreshness` (`server.go:167-189`); a stale row reads "last seen X ago", never "firing NOW" |
| `GET /api/insights` | the NOW surface: phenomenon match cards + cascade cards | MEASURED evidence with AUTHORED member notes **adjacent**, never fused |
| `GET /api/events` | discrete events (OOM, CrashLoop, ImagePull, eviction, PVC) | `unavailableEvents` note when the events lane is off |
| `GET /api/topology` | the workload graph + edge validity | flow edges appear once a persistent connection is held |
| `GET /api/warnings` | PROJECTED early warnings + the band | when forecasting is off, the lane states the **gate rule** (why it is dark), not silence |
| `GET /api/cross-service` | MEASURED fan-in cascades + the anticipatory PROJECTED chain | `BuildCrossService(nil,...)` honest-dark when flow discovery is off |
| `GET /api/root-cause-chain` | the ordered transitive chain (Cap B) | honest-dark via `BuildRootCauseChain(nil,...)` when off |
| `GET /api/departures` | PROJECTED band-departure anomalies (Cap C, off-digest) | gate-pending; renders its gate note |
| `GET /api/unexplained` | loud unmatched cards + the static blind-spot notice | blind spot is surfaced even when the channel is off |
| `GET /api/silence-ledger` | the durable absence ledger | `BuildSilenceLedger("",...)` honest "binding has not compiled" |
| `GET /api/incidents` | incident-identity grouping | `unavailableIncidents` when off |
| `GET /api/timeline` | MEASURED matches / unexplained / PROJECTED spans | `projectedLaneOffNote` when the projected lane is off |
| `POST /api/validate-claim` | the referee's honest-label verdict on a typed claim | advisory; `LabelledBestEffort` note when `--referee-enabled` is off (`server.go`) |
| `POST /mcp` | the relay tools (chain / insights / cross-service / topology / unexplained / departures / authored-relations) for the AI panel | **POST-only** — a GET 405s |

> **Implementation gotcha to carry forward (from the console work):** API arrays
> serialize as `null` when empty. The Streamlit read-back must guard every
> iterated array (`x or []`) or a panel will crash on a quiet cluster. And the
> MCP endpoint is **POST-only** — do not poll it with GET.

---

## 6. Safety rails

The cluster constraints are documented and non-negotiable
(doc 14 §3.1, `deploy/DEVELOPMENT.md`):

- **kind nodes share ONE ~405GB Docker VM disk**, and there are **3 nodes total**
  (1 control + 2 workers — the floor for topology/pressure phenomena) **sharing
  the host kernel**. There is no resource fairness between unrelated experiments.

Therefore the simulator enforces, as hard preconditions on **every mutating
action**:

1. **Bounded allocators only.** Every templated leak/creep rig MUST declare a
   `limits.memory` and the allocator MUST be bounded by it (mirroring
   `corpus/chaos/leak-oom.yaml`, `leak-creep-demo.yaml`, `chain3-rootcause.yaml`
   — all use explicit limits + bounded loops). The UI rejects an "unbounded"
   leak; there is no such control.
2. **Solo-rig execution.** At most **one** chaos rig runs at a time per cluster
   session. The launcher refuses a second rig while one is active (or requires
   an explicit teardown first). Rationale: shared disk + shared kernel + no
   fairness ⇒ high-density multi-rig runs cascade across unrelated experiments.
3. **Hard caps that can never OOM the VM or a node.**
   - Total templated memory across a rig is capped well under node allocatable
     **and** under the shared-VM headroom; the UI clamps slider maxima to the
     configured cap (§9).
   - **Disk headroom guard:** the simulator queries free disk on the target and
     **refuses any disk-fill rig** (and any heavy-OOM rig) unless headroom
     exceeds a configured floor (the docs require **>100GB headroom**; disk-fill
     remains UNSAFE on the shared local VM and should be **disabled by default**,
     enabled only against a real staging cluster of VMs — doc 14 §3.1).
   - The **OOM cascade toggle** is *off by default*; enabling it requires a
     confirm step, because past leak rigs OOM'd Docker. The creep-to-plateau
     path (no kill) is the safe default forecast demo.
4. **Dry-run mode (default ON).** In dry-run, the controller renders the exact
   manifest YAML and the exact `kubectl apply/patch/scale` arguments and
   **applies nothing**. The operator reviews, then disarms dry-run to execute.
5. **Auto-teardown.** Every rig is applied with an owner label and a TTL. On
   scenario end, on a timeout, or on app exit, the controller deletes everything
   it created (label-scoped `kubectl delete`), and verifies the namespace
   returns to baseline before declaring "torn down". A panic/disconnect triggers
   the same teardown.
6. **Node-pressure honesty.** Controls that drive node PSI / eviction /
   conntrack are labelled "realism limited on kind (shared kernel)"; the
   simulator never claims a node-pressure *falsification* on the local VM — that
   requires the staging cluster of real VMs (doc 14 §3.1).

---

## 7. Generalization & config

The simulator is **generalized**: the specific target cluster is chosen *after*
a sample cluster exists. Nothing about a service, namespace, or limit is
hardcoded. A single config file (or Streamlit sidebar form) parameterizes:

- **Cluster access:** `kubeconfig` path + context name. The controller talks to
  whatever that context points at.
- **Target namespace(s):** where chaos rigs are applied and which workloads the
  app-SLO / probe / image controls patch. (Shipped rigs pin `namespace: chaos`
  and `nodeSelector: vigil-worker` — these are **overridden** at apply time.)
- **Target service catalog:** the workloads available to patch for probe-fail,
  image-pull-fail, traffic, and the cascade chain. Discovered from the cluster
  (list Deployments in the target ns), not baked in. Online Boutique is the
  Phase-0 reference workload (~11 gRPC services with declared limits,
  `deploy/workloads/online-boutique.yaml`); the simulator can force one service
  **unbounded** to exercise the resolvability-hole / honest-gap policy from day
  one.
- **Node selectors:** which nodes pressure rigs land on (default the worker
  pool, never the control node).
- **Limits & SLO bars:** per-rig `limits.memory`, `limits.cpu`, and the
  app-SLO annotation values are **inputs**, not constants. The shipped numbers
  (e.g. 96Mi/320Mi limits, +6Mi/min, queue=100/age=60/rate=50) are *initial
  values from harness evidence* (doc 14 §5; doc 11) — the simulator exposes them
  as sliders/fields, it does not freeze them.
- **Vigil endpoint:** the base URL for `/api` and `/mcp` (default the local
  obsd address), plus which lanes are expected on (so panels can distinguish
  "honest OFF" from "broken").
- **Safety caps:** the per-rig memory cap, the disk-headroom floor, the
  solo-rig lock, and the dry-run default — all config, so a real staging cluster
  can raise caps that the local shared VM must keep low.

---

## 8. Caveats & honest boundaries

- This plan is **read-only research / design**; no implementation, modification,
  or file creation has occurred beyond this document.
- **Environment limits are real and must be surfaced, not hidden:**
  `container_oom_events_total = 0` on kind (OOM seen via the events lane +
  `lastState.terminated`, not the cAdvisor counter); **PVC FILL is unobservable
  on kind** (no `kubelet_volume_stats`); **PSI metrics need kernel ≥ 4.20**; the
  3 kind nodes **share one ~405GB disk and the host kernel**, so disk-fill and
  high-density OOM rigs are unsafe locally and node-pressure realism is limited.
  A staging cluster of real VMs (k3s/managed) is required for node-pressure
  falsification before production trust (doc 14 §3.1).
- **`IMAGE_PULL_FAILURE` is event-driven, not metric-driven.** The simulator
  induces it by patching a Deployment's image to a bad tag (requires the cluster
  to *attempt* and fail the pull); it is matched deterministically against the
  authored event-conditions overlay (`docs/15-capability-architecture.md` §8). It
  cannot be faked from a metric.
- **Capability maturity (label honestly in the UI):** Forecasting (PROJECTED)
  and App-SLO (Cap A) are live-wired. The transitive root-cause chain (Cap B)
  and the anticipatory cross-service cascade are live but each rig must be run
  **solo** and **sealed before OOM**. Multi-hop projected cascades (Cap D) and
  band-departure anomaly (Cap C, the `/api/departures` off-digest lane) are
  **gate-pending** — their panels render the verbatim gate note, never an empty
  card.
- **The charter forbids learned edges, weights, thresholds, and baselines
  anywhere.** Every bar the simulator drives a series toward is **borrowed** from
  the workload's own declared limits/SLO annotations or a flagged ontology
  default. A "smart" simulator that auto-tunes its parameters from what Vigil
  reported would launder class across the surfacing boundary and is explicitly
  out of scope.
- **Read-back preserves provenance labelling at the surface, never fuses.** A
  cross-service chain is shown with each hop labelled MEASURED (the edge + the
  node degradation) and the relation labelled AUTHORED, sitting **adjacent**;
  the PROJECTED band sits adjacent to both, labelled. The *join* — not a fusion —
  is the contract (doc 01; `obsd/internal/api/insights.go` `InsightCard` →
  `MemberRow` shows MEASURED evidence next to AUTHORED notes without merging).
- **All shipped chaos-rig timings are initial calibration values** (doc 14 §5),
  not magic constants. The simulator must expose prefill sizes, leak rates,
  pressure ramps, and window durations as controls — and must keep them within
  the safety caps of §6.
