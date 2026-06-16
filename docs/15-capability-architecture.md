# 15 — Capability Architecture: from coverage-width to chain-depth

> Status: **active forward plan** (authored 2026-06-16). Companion to the blueprint
> 00–14. This doc names the **vertical capabilities** Vigil needs to deliver
> root-cause + forecasting + anomaly across a real multi-service chain reaction, the
> charter discipline that keeps each honest, the validation harness each needs, and
> the build sequence. Grounded in the real `obsd` tree (file:line throughout).

## 0. The thesis — width vs depth

The foundation (docs 00–14) is a real general pipeline: `scrape → bind → detect →
forecast → surface`, with provenance (MEASURED/PROJECTED/AUTHORED) and
borrowed-normativity bars enforced in code and gated by byte-identical replay. That is
architecture, not patches.

But the recent cadence (v3 tracks, graph-robustness, events) has been **horizontal** —
more phenomena, the same reach. It made Vigil *wider*, never *deeper into a chain*.
Every bar in the system today is one of **four** k8s-resource limits
(`graph/overlay.go:43-49` `knownConfigPaths`: container mem/cpu, PVC storage, node
mem), so a failure that propagates through **application-level** numbers
(request-rate, queue-depth, write-latency, staleness) — and the multi-hop dependency
structure between services — is **end-to-end invisible**, and even where visible,
root-cause bottoms out at one-hop pairs (`flow/structural.go:46`).

Delivering operator value for a chain reaction requires four **vertical** capabilities.
Three of the four have a charter-violating "clever" version; §3 records which were
killed and why, so they are never re-attempted.

## 1. The driving use case (kept as a target, not waved off)

A smart-traffic K8s platform: `camera-ingestion → AI-inference → traffic-aggregation
→ database + cache → prediction`. Rush hour: ingestion gets **3× video load** →
inference CPU+mem spike + temp writes to storage → storage disk I/O + latency →
aggregation **queue buildup** → database **write-burst → restarts** → prediction runs
on **stale data → inaccurate forecasts**. Six links; looks like isolated CPU/storage/DB
issues; is one chain reaction.

| Link | Today (grounded) | Target capability |
|---|---|---|
| **L1** 3× load (trigger) | **blind** — no request-rate ingest; conntrack counts connections, not requests | **A** (request-rate vs declared capacity) + **C** (band-departure) |
| **L2** inference CPU+mem | **partial** — `MEMORY_LEAK` (degraded), `THROTTLING_CASCADE` (full if CPU limit declared), OOM via events | already wired; **D** forecasts the memory limb |
| **L3** storage I/O latency | **partial, degraded-only** — node IO-PSI fires; per-device/per-PVC blind | **A** (DB/storage exporter p99 gauge vs declared) |
| **L4** aggregation queue buildup | **blind** — queue-depth is an app metric | **A** (queue_depth vs declared) |
| **L5** DB write bottleneck → restarts | **partial** — restart *effect* via events; write-latency *root* blind | **A** (write-p99 vs declared, where a scalar gauge exists) |
| **L6** prediction on stale data | **blind** — data freshness has no signal | **A** (staleness-age vs freshness SLO); **correctness stays blind — §6** |
| **the chain** | **not connected** — one-hop fan-in only, finding-gated | **B** (transitive structural+authored reach) |
| **forecast the propagation** | **one hop** (`flow/crossservice.go`) | **D** (multi-hop inherited widening band) |

## 2. The non-negotiable disciplines (every capability threads these — doc 01)

1. **Never invent causation.** Co-occurrence / topological-match is never causal proof.
   A cascade is an AUTHORED edge lighting up, surfaced verbatim.
2. **No learned edge/weight/threshold/baseline anywhere.** Bars are BORROWED from
   customer config (declared limits/SLOs) or flagged ontology defaults. "3× normal" via
   a learned baseline is **forbidden**.
3. **Provenance classes joined, never fused; weakest-input rule** (the result of a
   computation is no stronger than its weakest input).
4. **Determinism.** Same readings + graph version + topology ⇒ byte-identical findings;
   replay-gated. The clock proto carries no string fields.

## 3. The two killed approaches (recorded so they are never re-attempted)

- **"Infer causal direction from timing"** (the naïve transitive root-cause). KILLED.
  (i) It promotes co-occurrence-along-edges + precedence to a causal verdict — exactly
  doc 01's forbidden `cascade → inferred causation`. (ii) The signal does not exist:
  `observe/fingerprint.go:154` stamps *every* fingerprint in a tick with the same
  `evalNow`, so there is zero intra-tick onset to read. Direction is therefore decided
  **only** by an authored relation, never by timing.
- **"Anomaly = forecast-band departure, surfaced MEASURED in the unexplained channel."**
  KILLED as proposed. (i) `replay/digest.go:40` hashes the unexplained findings — a
  PROJECTED band edge entering the digest **breaks byte-replay**. (ii) The band edge is
  PROJECTED; calling the comparison MEASURED **launders PROJECTED→MEASURED**. Departure
  survives only **off-digest, classed PROJECTED**, in its own lane.

## 4. The four capabilities

### A — Application-signal ingestion *(the keystone; build first)*
The whole chain is invisible without it (L1/L4/L5/L6). It is the SAME pipeline pointed
at app `/metrics`, with **customer-declared SLO bars**.

- **Extension points (each clones a proven lane):**
  - FETCH: `kube.PodMetricsFetcher` beside `ProxyFetcher` (`kube/proxy.go:23`) — GET
    `namespaces/<ns>/pods/<pod>:<port>/proxy/<path>`, the sibling of the `nodes/proxy`
    subresource already used. Targets from the customer's own `prometheus.io/scrape`
    annotations.
  - NORMALIZE: `identity.FamilyApp` (`identity/normalize.go:22` enum) + a route in
    `ingestExposition` (`observe/scrape.go:176`); identity resolved from the **scrape
    target's pod UID** (the node-exporter-style "identity from target" rule). Extend
    `streamSubID` (`scrape.go:275`) to cover `FamilyApp` (heavily-labelled app series
    must not collapse into one ring — a mis-join that breaks correctness *and* replay).
  - BIND: extend `knownConfigPaths` (`overlay.go:49`) with `slo.*` paths + a
    `readSLOPath` branch mirroring `readContainerPath` (`binding/compile.go:257`).
- **SLO declaration model (borrowed normativity):** the customer declares the bar on
  their **own object** — a workload **annotation** `vigil.io/slo.<metric>: "<value>"`
  (the same shape as reading `resources.limits`), read at bind time. Undeclared ⇒
  `unbounded`/listed (the exact `compile.go:163-165` contract). **Forbid the default-bar
  branch for *capacity/load* paths** — a "default capacity" *is* the learned baseline the
  charter bans. (A flagged ontology default may be defensible for *freshness/queue*
  absolutes, never for *load*.)
- **Staleness uses the injected eval clock:** `value = EvalNow − last_update_timestamp`
  is MEASURED only because `EvalNow` is a replayed tick fact. Any `time.Now()` there
  fuses wall-clock with a stored fact and breaks replay.
- **KG authoring:** app-signal phenomena (RED / queue / freshness) authored via overlay
  with config-relative SLO rules + checks — the same governed path as every other
  phenomenon; the chain edges are AUTHORED `phenomenon_relation`s, never derived.
- **Harness — `just app-slo-gate`:** JOIN correctness (label oracle + quarantine count),
  BORROWED-bar provenance (**a test that fabricating a bar from traffic FAILS**),
  determinism (a 3× capture replays byte-identical incl. staleness via injected clock),
  and a **near-miss negative** (a legit burst *under* declared capacity must NOT fire).
- **Honest ceilings:** (a) L5 via histogram is **not buildable** — `scrape.go:30`
  counts-and-skips histogram/summary families; L5 works only where the DB exporter
  exposes a *scalar p99 gauge*. (b) The lane proves **freshness, not correctness** (§6).

### B — Transitive structural+authored root-cause *(ship the reach; the temporal headline is dead — §3)*
- **Extension point:** new `flow/transitive.go` beside `StructuralCascade`
  (`structural.go:35`), reusing `NeighboursInto(EdgeTypeFlow,…)`, called from the
  warm-path block (`cmd/obsd/main.go` where `CrossServiceChain` runs), **strictly
  off-digest**. Output extends `flow.Chain` (`chain.go:13`) with an ordered `Path`.
- **What ships (charter-clean):**
  1. **Direction is NEVER decided by timing.** An edge is oriented upstream→downstream
     only if (a) it is an observed-flow edge (valid/suspect) AND (b) an AUTHORED relation
     declares trigger→downstream for that adjacency. No authored relation ⇒ the path
     **breaks** there and the gap is stated. This is `BlastRadiusFor` made transitive.
  2. **Silent intermediates are traversed for connectivity but never *asserted*** — the
     asserted path ends at the last MEASURED-degraded node; the silence is a stated gap,
     never a bridge (weakest-input rule).
  3. **Determinism:** off-digest; own total order over the full path-key (Go map
     iteration is randomized); visited-set cycle terminator; `maxHops` param ceiling.
  4. **Surface = JOIN**, scanned by `HasForbiddenToken` (`chain.go:64`); the only `why`
     is the verbatim authored note.
- **Harness — chain-reconstruction gate:** ROOT==structural-root, PATH==authored-reach,
  silent-not-asserted, charter scan; **cardinal rule: ZERO false chains on
  independently-coincident faults**. Byte-identical (off-digest, pure fn of inputs).

### C — Anomaly primitive *(capacity-crossing ships now; departure off-digest, PROJECTED)*
- **Capacity-crossing (clean, ships with A):** a second `BarSource` from declared
  limits/SLOs flowing the unchanged `t.State.Crossed()` path. MEASURED-vs-MEASURED, no
  clock, byte-identical. Undeclared ⇒ `unbounded`/listed.
- **Departure-from-own-forecast-band (salvaged off-digest):** a **new producer behind
  `--departure-enabled`**, its own surface + gate, **zero bytes into `replay.Digest`**
  (exactly how the events lane stays off-digest). Classed **PROJECTED** (band = PROJECTED
  context, realized sample MEASURED, joined-not-fused). Never feeds governance/candidate
  aggregation (a model-derived signal must not influence authored graph edges). Structural
  FP defense: a noisy-but-stationary series gets a *wide* band ⇒ stays inside.
- **Harness — `departure_gate`** over the **near-miss/decoy corpus (#75)**: FP-rate on
  decoys ≤ ceiling, recall on the true step; determinism scoped honestly ("same recorded
  clock traces + params ⇒ same flags" — re-run + diff JSONL, **not** tick-digest).

### D — Multi-hop projected cascade *(forecast the ripple)*
- **Extension point (3 off-digest seams):** the eligibility switch
  (`forecast/eligibility.go:124`) admits a latency/queue **gauge level** only when a
  declared bar resolves; `ProjectedCrossServiceChain` (`flow/crossservice.go`) swaps its
  single cascade call for a **bounded transitive closure** (`maxHops` param); the
  workload struct gains hop-distance + a widened band.
- **Charter discipline:**
  1. **ONE forecast root per chain.** Exactly one callee passed the funnel + crossed its
     *own* declared bar; every other node is a caller reached over MEASURED flow edges,
     carrying the **inherited (widening) band** as a PROJECTED-impact hypothesis. **Never
     re-run the clock per hop** (stacks errors, manufactures a causal chain).
  2. **No learned bar** (admit only `b.Bar != nil`, kind ≠ rate-of-change, no derived
     ratio — already deferred at `eligibility.go:109-116`).
  3. **Band-widening is a PRODUCER INVARIANT:** assert `earliest_child ≤ earliest_parent`
     and `latest_child ≥ latest_parent` at construction; a downstream node tighter than
     its parent is PROJECTED-dressed-as-stronger — an absolute-zero failure.
  4. Deterministic structural skeleton (total order per BFS level, visited-set, maxHops).
  5. `HasForbiddenToken` over the whole chain; add `propagates`/`cascades-to`/`flows-to`.
- **Harness — extend `projected-crossservice-gate`:** per-hop lead, full-chain fidelity,
  **band-monotonicity as absolute-zero**, ≥2 confirmed chains + quiet ticks; confirmation
  must be **per-root-with-full-chain**; needs a **real 2-hop capture on kind** (stays dark
  until a genuine 2-hop lead+confirm is observed).

## 5. The harness as a whole — four new gates, same discipline

Each follows the proven template: a `replay -<mode>` pass (recompute, verify nothing) →
JSONL → a Python scorer over a **frozen labeled corpus** with **exact floors** +
**INSUFFICIENT-never-passes** → a `just` recipe.

| Gate | mode | cardinal rule | determinism claim |
|---|---|---|---|
| app-slo (A) | `-appslo` | fabricating a bar from traffic FAILS | byte-identical (incl. staleness via injected clock) |
| chain-reconstruction (B) | `-transitive-chain` | ZERO false chains on coincident faults | byte-identical (off-digest, pure fn) |
| anomaly-FP (C) | `-departure` | decoy (wide-band) must NOT depart | "same clock traces + params ⇒ same flags" (re-run + diff) |
| multi-hop lead (D) | extend `-projected-crossservice` | band narrowing at any hop = instant fail | JOIN-verified (PROJECTED off-digest by class) |

**Honest harness boundary, stated in every verdict:** A and B certify *byte-identical*
determinism; C-departure and D certify the *JOIN*, not the tick-digest — because their
substrate (the clock) is non-deterministic by class. A gate claiming digest-determinism
for a clock-derived lane would be lying.

## 6. What the operator sees — and what stays honestly blind

During the rush hour, one screen, three provenance-labelled clauses, never a fabricated
"X caused Y":
- **NOW** — `inference (working_set over mem-limit) → aggregation (queue_depth over
  slo.queue.max_depth) → db (write-p99 over slo.latency.write_p99) → prediction
  (staleness-age over slo.freshness.max_age)`, walked over OBSERVED-flow edges
  (`valid|suspect`), each hop carrying the verbatim authored `why` (author+version).
  Asserts adjacency + authored-relation + per-node measured degradation — **not** cause.
  Silent intermediates are stated gaps.
- **TRIGGER** — ingestion request-rate **departs its own band minutes early** (PROJECTED)
  and/or **crosses declared capacity** (MEASURED). The departure card auto-supersedes when
  the authored cascade explains it.
- **SOON** — TimesFM warns the leaking callee; the projected impact ripples the chain
  **~10+ min ahead**, every hop a **widening band that never collapses**.

**Honest blind floor (charter, not laziness):**
- **True data-CORRECTNESS** — Vigil MEASURES that prediction runs on *stale* data (age
  vs declared freshness SLO); it **cannot** tell that an *on-time* value is *wrong*
  (silently corrupted, schema-drifted). Not threshold-crossable; **cannot** be made
  charter-clean. Freshness is observable; correctness is not. Do not promise it.
- **Causal direction no human authored** — the chain breaks and says so. A feature.
- **Histogram-only latency** — out until quantile sub-variable normalization (doc 02 §8).
- **C-departure and D are off-digest** — replay-verified at the JOIN, not the digest.

## 7. Sequencing & increments

**Build order: A → B → D → C** (capacity-crossing of C rides along with A). *Ingest the
chain → stitch the chain → forecast the chain → flag its earliest tremor.* Each is a
flagged, gated, charter-clean, replay-preserved lane; value compounds; risk is amortized
one lane at a time.

- **A** increments: (A1) app-metrics fetch+normalize lane (FamilyApp, flagged, fixture
  tested) → (A2) SLO-declared config-relative binding (annotation source, no-default-for-
  capacity) → (A3) app-signal phenomenon + detection → (A4) app-slo-gate → (A5)
  live-verify (synthetic app exposing `/metrics` + an SLO annotation on kind).
- **B** then **D** then **C-departure**, each: build → gate → live-verify → commit.

## 8. Status

- 2026-06-16: plan authored (this doc). Foundation + horizontal coverage complete; the
  one-hop cross-service now+soon cascade is live-proven (`f3c24b2`), so **D stands on
  certified ground**. **A is the active build** (the keystone). B/C/D follow in order.
