# Brutal-test campaign — June 2026

> The record of the "test Vigil pin-to-pin, on something ABB actually faces" campaign:
> what changed, what we did, and the live results. Companion to the test-strategy suite
> ([README.md](README.md)) and the cloud plan ([cloud-plan-aws.md](cloud-plan-aws.md)).
>
> One-line verdict: the deterministic core holds on pathological input AND under a live
> ABB-shaped fault loop on real k3s — it catches the true fault, names its bar's
> provenance, names what it can't see, and refuses the tempting-but-unproven causal
> story. The testing also *found real bugs* (a zero-node panic; 18-day cluster rot).

---

## 1. Why this campaign

A hackathon judge breaks a system two ways: feed one module a pathological input, or
break the whole cluster and watch the system lie about it. We attacked both axes, and
deliberately on an **ABB-shaped industrial-edge** workload (MQTT broker + historian on
a PVC + device-sim fleet + edge inference + SCADA) rather than the e-commerce demo —
because the product claim is *restraint* (what it refuses to assert), and that only
shows under an industrial fault sequence.

The standard for every new test was **teeth**: a deliberately-broken implementation was
constructed and confirmed to make the test fail. A test that passes against a broken
impl proves nothing.

---

## 2. What changed (by area)

### 2.1 v4 hardening merged into v5
Ten hardening commits had been stranded on branch `v4` (never merged to `main`/`v5`):
bearer-token auth + deny-by-default on `/api`+`/mcp`, schema-migration ladder,
scale benchmarks, e2e/scale/soak/forecast `just` recipes, corpus drift-guards, and the
four **structural meta-gates**. Cherry-picked onto `v5`, conflicts reconciled keeping
both v5 features and v4 hardening.

### 2.2 The structural meta-gates (now enforce *everyone's* code)
Four gates make it impossible to ship a feature untested:
- **API route guard** — every `/api` route must carry an explicit charter disposition.
- **MCP tool guard** — every MCP tool must be pinned, advertised, and dispatched.
- **Gate-flag registry** — every `…GatePassed` flag must map to a real `just` recipe.
- **Package-test presence** — every `obsd/` package needs tests or a documented exemption.
- Plus `TestNoTimeNowInLogic` — forbids `time.Now()` outside an injected-clock seam.

Proven live: mid-campaign a teammate's pull merged ~2,860 lines of doc-21 Phase 3–5
into v5; the charter gate immediately flagged their unclassified `/api/governance/preview`,
which we then classified. The discipline holds on incoming features, not just ours.

### 2.3 Brutal worst-case / property / fuzz suite (`373c968`)
A 24-agent authoring pass + an 18-agent adversarial-review pass added hermetic tests
across every previously-thin package: identity mis-join (property), detect metamorphic
(co-occurrence-never-cause), the replay **non-gating byte-identity** test (the charter's
absolute rule — *previously untested*), and fuzz/boundary across audit/trace/logtmpl/
qss/unexplained.
- **Found + fixed a real production bug:** `obsd/internal/kube/facts.go` ranged
  `nodes.Items[1:]` unguarded → **panic on a zero-node cluster**. The authoring agent had
  *skipped* the test; the adversarial pass caught the skip, forcing the bug into the open.
- **Fixed a charter violation:** `kube/proxy.go` read `time.Now()` in logic → injected a
  clock; added the lint gate above so it can't return.

### 2.4 ABB industrial-edge digital twin + chaos corpus (`4db92fd`)
`deploy/workloads/industrial-edge/` — MQTT broker, historian (StatefulSet+PVC), device
sims (current/power/temperature/vibration ×3), edge inference, SCADA. `sim-vibration` is
deliberately *unbounded* (no limits) to test the honest-null path. Chaos scenarios in
`corpus/chaos/industrial/` map to ABB's literal questions (leak→OOM→reconnect→CPU,
burst→throttle, unbounded workload, PVC-fill→forecast, PVC-I/O→restart).

### 2.5 io-scada coupling made real (`89fe357`)  ← this turn
The PVC-I/O→restart scenario was known-broken: the historian never listened on :8080, so
the SCADA restart fired from *connection-refused*, not disk saturation — a fabricated
link. Fixed: the historian now **listens on :8080 and serves each query as a genuine cold
device scan** of a 3 GiB on-disk TSDB (`posix_fadvise(DONTNEED)` → the read must hit the
block device), while a concurrent fsync write flood keeps node PSI high. Validated live
(§3.1). Honest rig caveats now documented in the manifest + label.

### 2.6 Fixed real cluster rot: the dead `metrics.k8s.io` APIService  ← this turn
The live scale test kept failing on a *ghost* `scale-load` deployment. Root cause: the
`v1beta1.metrics.k8s.io` APIService had been **`MissingEndpoints` for 18 days** (k3s
metrics-server failing on the kubelet's self-signed cert), and that broken aggregated API
was blocking namespace finalization — every namespace delete silently **orphaned its
contents**. Removed the dead APIService; namespace GC now completes in ~8s. (metrics-server
itself was patched with `--kubelet-insecure-tls`; a residual RBAC 403 is a k3s embedded-
kubelet quirk, out of scope — Vigil doesn't use metrics.k8s.io; it reads KSM + the kubelet
proxy directly.) This is exactly the silent rot the campaign is meant to surface.

### 2.7 Cloud bootstrap for AWS/Azure (`2e55c26`, `cb85757`)  ← this turn
`deploy/cloud/bootstrap-vigil-edge.sh` (one command: k3s `--max-pods=300` + toolchain +
obsd build + node-exporter/KSM/ABB-twin + tiers) and `cloud-runbook-aws-azure.md`. See
[cloud-plan-aws.md](cloud-plan-aws.md).

---

## 3. Live results (the proofs, on real k3s, 2026-06-20)

### 3.1 io-scada — the coupling is now physical
A manual in-cluster query against the historian Service:
- **CONNECTED in 0.071s** (not connection-refused — the historian listens),
- then **recv BLOCKED 3.98s** on the cold device read, returning the full 3 GiB,
- → exceeds the SCADA reader's 2 s budget → `/healthz` 503 → **restartCount 0 → 6**,
  `exitCode 137` via *"Liveness probe failed: HTTP 503"* (not OOM, not refused).

What Vigil reported (the charter payoff): the only finding it raised on the namespace was
the **scada-reader's own CPU throttle** (`PHEN_THROTTLING_CASCADE`, single Container entity,
`degraded`, completeness 0.5, the unobserved member named). It did **NOT** mint a
storage→restart or throttle→restart cause. `STORAGE_SATURATION` stayed silent (PSI needs
node-exporter, absent on the bare box — Vigil refuses to invent it); `PROBE_FAILURE_RESTART`
stayed below its coverage floor (only the KSM restart counter is metric-exposed). **Honest
silence over a fabricated match — that restraint is the result.**

### 3.2 Scale — hermetic cost curve
`go test -bench` (deterministic, no cluster):

| entities | detection/tick | | findings/tick | upsert/tick |
|---:|---:|---|---:|---:|
| 100 | 1.28 ms | | 10 | 0.28 ms |
| 500 | 6.64 ms | | 100 | 1.42 ms |
| 1,000 | 13.4 ms | | 1,000 | 12.8 ms |
| 5,000 | **62.75 ms** | | | |

Detection is **linear ~12.5 µs/entity** (5,000 entities = 63 ms — <0.5 % of a 15 s tick);
findings upsert **linear ~12.8 µs/finding**. The per-tick ceiling is well-characterized.

### 3.3 Scale — live (`TestLiveScale`, N=65)
- baseline **45 entities, RSS 95 MiB** → at scale **110 entities (+65), RSS 129 MiB**
  (+34 MiB, **~549 KiB/entity** — bounded),
- **0 mis-joins at 110 entities** (the load-bearing integrity assertion),
- churn to 0 → entities returned to ~baseline, **0 mis-joins** after churn. **PASS (83 s).**

(Live ran at 110 because the laptop k3s caps at `--max-pods=110` and lacks passwordless
sudo to raise it. Hundreds-of-pods live is the cloud run — see [cloud-plan-aws.md](cloud-plan-aws.md).
The hermetic curve already covers detection to 5,000 entities.)

### 3.4 Soak — RSS plateau + zero drift (`TestLiveSoak`, 30 min)
Against the steady digital-twin: **RSS 96 → 137 MiB peak**, oscillating, **misjoins 0**
across the full window — the textbook "qss rings fill, then plateau," far under the 2.5×
growth ceiling. No slow leak, no identity drift over time.

---

## 4. Methodology notes

- **Determinism-first:** golden fixtures, `go test -race`, injected clocks (no `time.Now`
  in logic — now lint-enforced). Same readings + graph version + topology ⇒ same digests.
- **Agent contamination is real:** the adversarial review caught genuine vacuous tests AND
  a stray probe-mutation an agent left behind — which is why the authoritative signal is
  always a fresh `go test -race ./...`, never an agent's self-report.
- **Honest partial coverage everywhere:** unobservable members are *named* on the finding
  (cAdvisor OOM counters, node PSI, probe internals), never back-filled with an invented bar.

---

## 5. Open items / what's next

1. **Cloud single-node run (AWS)** — the "hundreds of pods + real PSI saturation" encore
   on an `m6i.2xlarge`; see [cloud-plan-aws.md](cloud-plan-aws.md). Needs an AWS account +
   ~a few dollars of spend.
2. **node-exporter locally** — deploying it on the laptop k3s would let `STORAGE_SATURATION`
   fire for the io-scada scenario locally too (currently honestly silent).
3. **`PROBE_FAILURE_RESTART` from the KSM counter alone** — today it needs probe-internal
   members the kubelet doesn't expose; worth a graph review on whether the restart counter
   should surface a degraded match on its own.
