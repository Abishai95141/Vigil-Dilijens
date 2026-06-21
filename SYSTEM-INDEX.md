# Vigil — System Index

> A whole-system intelligence map: what exists, how each part works, why it exists, and
> how the pieces compose into one operational reasoning layer. This document traces the
> codebase module by module, explains the mathematics and heuristics in context, and
> states the rationale behind each design choice. It is the capstone over the numbered
> design suite in [`docs/`](docs/) and the per-package contracts in each `doc.go`.

**Repository:** branch `final` · Go modular monolith (`obsd`) + Python forecasting clock
(`clockd`) + Python validation harness (`harness`) + curated ontology (`ontology`,
release **v0.13.0**, `sha256:6c9e75be…`) + React operator console (`console`).

---

## Table of contents

1. [The thesis — what Vigil is](#1-the-thesis--what-vigil-is)
2. [The charter — the one load-bearing constraint](#2-the-charter--the-one-load-bearing-constraint)
3. [System topology — the map](#3-system-topology--the-map)
4. [The deterministic spine — the "now" engine](#4-the-deterministic-spine--the-now-engine)
5. [The forecasting clock — the "next" engine](#5-the-forecasting-clock--the-next-engine)
6. [The anomaly and causal lanes — off-digest inference](#6-the-anomaly-and-causal-lanes--off-digest-inference)
7. [The correlation and modality lanes — joining distinct sources](#7-the-correlation-and-modality-lanes--joining-distinct-sources)
8. [The propose→verify→promote spine — growth without corruption](#8-the-proposeverifypromote-spine--growth-without-corruption)
9. [The surfacing layer — where the classes meet](#9-the-surfacing-layer--where-the-classes-meet)
10. [The ontology graph — the curated knowledge](#10-the-ontology-graph--the-curated-knowledge)
11. [The validation harness — falsification as a gate](#11-the-validation-harness--falsification-as-a-gate)
12. [The orchestrator — the tick loop](#12-the-orchestrator--the-tick-loop)
13. [Cross-cutting invariants](#13-cross-cutting-invariants)
14. [Why this design suits the environment](#14-why-this-design-suits-the-environment)
15. [Honest boundaries — what Vigil deliberately cannot do](#15-honest-boundaries--what-vigil-deliberately-cannot-do)
16. [Appendix — package index](#16-appendix--package-index)

---

## 1. The thesis — what Vigil is

Vigil is a Kubernetes-native observability engine that does three things and refuses to
pretend it does more:

- **Detects** current failure *phenomena* across the cluster's topology — not single
  metric crossings, but authored patterns of co-occurring states across related entities.
- **Forecasts** imminent threshold crossings on a deliberately scarce set of series,
  with an uncertainty band that never collapses to false precision.
- **Surfaces** both — plus the things it is *not* watching — under a strict epistemic
  discipline, so an operator can always trust what a finding means and where it came from.

The problem it targets is the one that makes most monitoring stacks untrustworthy under
pressure: they fuse measured facts, learned baselines, and human intuition into a single
opaque "alert" that an operator cannot decompose, reproduce, or audit. When the alert is
wrong — a learned baseline drifted, a correlation was mistaken for a cause, a threshold
was invented — the operator has no way to see *why*. Vigil's answer is architectural:
**every statement the system makes carries the class of knowledge it came from, and the
classes are never silently merged.** A measured crossing is shown as measured; a forecast
is shown as a projection with a band; an authored relationship is shown attributed to its
curator. The "why" is always the graph's authored knowledge, quoted — never a sentence the
machine generated.

The result is a system that is *narrower* than a typical AIOps product and far more
honest about its edges. It is a symptom detector plus a dependency graph plus a narrow
forecaster — and it tells you, by construction, exactly what it cannot see.

---

## 2. The charter — the one load-bearing constraint

Almost every design decision in the codebase is downstream of a single discipline,
specified in [`docs/01-epistemic-separation-charter.md`](docs/01-epistemic-separation-charter.md)
and enforced mechanically throughout. Understand the charter and the rest of the system
becomes legible.

### 2.1 Three provenance classes that join, never fuse

Every datum in the system is exactly one of:

| Class | Meaning | Register at surface |
|---|---|---|
| **MEASURED** | A deterministic arithmetic consequence of readings (a threshold state, a rate, a co-occurrence, a correlation, an observed network flow). | indicative — "is at / crossed" |
| **PROJECTED** | A model's forecast of the future. Always carries a non-collapsing quantile band. | explicitly modal — "projected to cross", never "will" |
| **AUTHORED** | A human-curated assertion in the ontology (a phenomenon definition, a causal relationship, a default threshold). | attributed — quoted verbatim with author + version |

The cardinal rule is **join, never fuse**. When these classes meet — they only meet in
one place, the surfacing layer — they are composed *adjacently*, each keeping its label.
A finding is "these MEASURED states co-occurred in this AUTHORED pattern"; it is never
fused into a generated causal sentence like "X caused Y." There is a fourth, strictly
quarantined class, **ADVISORY**, that exists only at the MCP boundary (§9): an LLM's
synthesis, labelled as such, gated behind a backtest, and never allowed to wear a
MEASURED/PROJECTED/AUTHORED badge.

### 2.2 The four prohibitions

1. **Never invent causation.** Causal *direction* is only ever AUTHORED by a named human.
   Correlation, co-occurrence, observed flow direction, and temporal adjacency are all
   MEASURED facts that are surfaced *as* correlation/adjacency — never upgraded to cause.
   The words "cause/caused/root-cause" appear nowhere in the output of any inference lane.

2. **Never invent thresholds (borrowed normativity).** A bar comes from the customer's own
   declared configuration (a resource limit, an SLO annotation) → an operator override →
   an ontology default that is *always flagged* as lower-trust. Where the customer declared
   nothing, the series is **unbounded and listed as such** — never silently assigned a
   learned threshold. No cutoff is ever fit to the data it is applied to.

3. **Never learn per-customer state.** The entire statistical vocabulary of the
   deterministic path is three primitives (threshold, rate, co-occurrence). No baselines,
   no seasonal models, no distribution fits, no anomaly scores live on that path — by
   commitment, because each would reintroduce hidden per-customer learned state.

4. **Never let the model gate the engine (non-gating).** Detection is byte-identical
   whether forecasting, the agent, alerting, and every modality lane are present, degraded,
   or absent. The model answers one question over bare floats; it never perturbs a finding.

### 2.3 Determinism by construction

The deterministic path obeys: **same readings + same resolved bars + same graph version +
same parameters ⇒ the same fingerprints and findings, always.** This is not an aspiration;
it is mechanically verified. Each live tick computes a SHA-256 digest of its results and
records it; the replay engine re-runs a captured bundle through the *real* components and
recomputes the digest from disk. A mismatch fails loudly. This is what lets an operator
trust a finding under incident pressure — it can be reproduced byte-for-byte from pinned
inputs, with no hidden state and no nondeterministic ML in the loop.

### 2.4 The firewall

Everything that is *not* deterministic — forecasting, the LLM agent, association, causal
hypotheses, the modality lanes (events, traces, audit, logs, flow), alerting — lives
**off the digest**, behind a firewall that is enforced by tests, not convention. A suite of
`firewall_test.go` files runs `go list -deps` over the deterministic packages and fails the
build if any of them transitively imports an off-digest package. Candidate knowledge lives
in a separate `candidates.db` and in an overlay subdirectory the graph loader structurally
skips. The deterministic path *cannot* read speculative data even by accident.

---

## 3. System topology — the map

```
                          ┌─────────────────────────────────────────────────┐
   Kubernetes cluster     │                     obsd (Go)                     │
   ┌───────────────┐      │                                                   │
   │ cAdvisor      │─────▶│  ① DETERMINISTIC SPINE (the digest)               │
   │ node-exporter │ scrape│   identity → qss → observe → binding → selection │
   │ kube-state-m. │      │            → detect → unexplained → replay         │
   │ app /metrics  │      │                       │                            │
   └───────────────┘      │                       │ Tier-B targets (snapshot)  │
                          │                       ▼                            │
   ┌───────────────┐      │  ② FORECASTING (off-digest, non-gating)  ──────────┼──▶ clockd (Python)
   │ K8s Events    │─────▶│   forecast: eligibility→budget→decompose→          │   TimesFM 2.5 /
   │ OTel spans    │─────▶│   regime-shift→trend→calibrate→project             │   stub clock
   │ audit log     │─────▶│                                                    │   (bare floats only)
   │ conntrack     │─────▶│  ③ INFERENCE LANES (off-digest, firewalled)        │
   │ logs          │─────▶│   onset(CUSUM) · departure · assoc · cohypothesis  │
   └───────────────┘      │   flow · incident · events · trace · audit · logs  │
                          │                                                    │
                          │  ④ PROPOSE→VERIFY→PROMOTE                          │
                          │   dgx(agent) → candidate(firewalled db) →          │
                          │   governance(gates) → AUTHORED overlay             │
                          │                                                    │
                          │  ⑤ SURFACING (the one join point)                  │
                          │   api (net/http JSON, 30 routes) · mcp · notify    │
                          └───────────────────┬────────────────────────────────┘
                                              │ /api  /mcp
                          ┌───────────────────▼────────────┐   ┌──────────────────┐
                          │ console (React/Vite/TS)         │   │ harness (Python) │
                          │ 14 operator surfaces            │   │ 21 falsification │
                          └─────────────────────────────────┘   │ gates            │
                                                                 └──────────────────┘
```

**The language split is deliberate.** Go owns everything that must be deterministic,
CGO-free, and cross-platform-identical (the engine). Python owns the two things that are
inherently statistical and where the ecosystem is strongest: the TimesFM forecasting model
(`clockd`) and the offline falsification/backtest suite (`harness`). The two are connected
only through a semantics-free contract (§5.1). The ontology is data — a curated, versioned
JSON graph plus authored YAML overlays. The console is a thin typed client over the API.

**Why a modular monolith and not microservices.** The engine's correctness depends on every
evaluation tick observing a *complete, consistent* snapshot of readings and topology. A
single process with one store gate (§12) gives that for free; a distributed system would
have to reconstruct the consistency guarantee at far higher cost, and would put the
determinism contract at the mercy of network ordering. The internal package boundaries are
strict (each with a `doc.go` charter), so the monolith is modular in every sense that
matters except deployment.

---

## 4. The deterministic spine — the "now" engine

This is the heart: a pure-function pipeline from raw readings to findings, replayable
byte-for-byte. Data flows
**identity → qss → observe → binding → selection → detect → unexplained → replay**.

### 4.1 `identity` — the join key, and the silent killer it defends against

Owning doc: [`docs/03`](docs/03-identity-and-correlation-layer.md). Package:
[`obsd/internal/identity`](obsd/internal/identity).

Identity is "prerequisite zero." Before any number can be evaluated against any bar, the
stream carrying that number must be joined to the right entity in the graph. A *mis-join* —
binding a stream to the wrong entity — corrupts every downstream behaviour invisibly. The
package's entire design is structured around one principle: **join by exact match on a
canonical key, or quarantine; never guess.**

**The Canonical Entity Identity (CEI)** is the only join key in the system
(`cei.go`). It is built from stable coordinates and serialized with a reserved `|`
separator:

```
Instance layer:  i|<cluster>|<namespace>|<kind>|<name>|<uid>
Role layer:      r|<cluster>|<namespace>|<kind>|<roleKey>
```

The `i`/`r` prefixes put instance and role keys in disjoint namespaces so they can never
collide. `cluster` is the kube-system namespace UID — the cluster anchor on every CEI.
Every identity-bearing coordinate must be non-empty and separator-free, or minting is
rejected. Provenance fields (`MintedAt`) are deliberately excluded from the key, so
re-minting the same coordinates yields an *equal* identity.

**Role derivation** (`DeriveRole`) anchors on the *topmost* controller in the
OwnerReference chain (Pod → ReplicaSet → Deployment). This is why a rollout — which mints a
new ReplicaSet and new pods — does **not** mint a new role: the role survives churn. This
single decision is what makes churn-stable forecasting and cross-restart incident identity
possible (§5.7, §7.2). Static/mirror pods are the one documented exception (their kubelet
ownerRef points at the Node, which would otherwise collapse every control-plane component
on a node into one role).

**Normalization** (`normalize.go`) is the single point where each exporter family's dialect
(cAdvisor, kube-state-metrics, node-exporter, app `/metrics`) collapses into CEI
coordinates. The join is *time-aware*: a sample joins the entity that was alive **when the
sample was taken** (its event time), not a same-named successor — because cAdvisor stamps
samples seconds-stale and a naïve join would attach a dead pod's reading to its
replacement. Anything that cannot be normalized has exactly one of three outcomes, each
carrying a stated reason: `Resolved`, `Dropped` (recognized and deliberately not modeled,
e.g. the pause sandbox), or `Quarantined` (expected to join, couldn't). Quarantined streams
are counted and surfaced, never silently discarded and never guessed into place.

**The lifecycle store** (`lifecycle.go`) tracks discovered → active → terminated with
*succession*: a new same-name-different-UID instance closes its predecessor's interval,
guaranteeing non-overlapping name intervals even if a delete event was missed. Two-tier
tombstones (full record for ~15 min, correct-CEI stub for ~24 h, then treated as absent at
read time) defend the trust-critical late-sample window. An LRU eviction *inside* the
retention horizon is counted as a join-risk signal, never silent.

**The mis-join auditor** (`audit.go`) continuously cross-checks the identity model against
an *independent* truth source (raw informer listers, not the store itself), so a divergence
is a real bug rather than a self-consistent echo. The Phase-0a exit gate passes only if
**mis-joins == 0** and coverage meets target — and an unsynced informer (empty read) can
never pass, so a startup race cannot publish a false all-clear.

**Why this is right for the environment.** Kubernetes is a churn machine: pods are cattle,
names are reused, exporters lie about timestamps, and the delete event you needed may have
been dropped. Fuzzy matching in that environment is not a convenience — it is a guaranteed
source of silent, unfalsifiable corruption. Exact-match-or-quarantine trades coverage you
can measure (the quarantine count) for correctness you can trust.

### 4.2 `qss` — the Quantitative State Store

Owning doc: [`docs/05 §3.1`](docs/05-observation-and-fingerprint-pipeline.md). Package:
[`obsd/internal/qss`](obsd/internal/qss).

A deliberately *thin* time-series store that stores and retrieves raw numbers, one stream
per (CEI, variable), and **never interprets them**. Two tiers:

- **Hot ring** (in memory): one fixed-capacity ring per stream, **240 records = 60 minutes
  at the 15 s scrape cadence**. The only thing the hot path ever reads; it never touches
  disk. The capacity is *digest-bearing* (pinned in the replay manifest) because it bounds
  what a window evaluation can see. `Latest` scans for the timestamp-newest sample rather
  than the last-appended one, because exporter timestamps regress across scrapes.
- **Warm segments** (on disk): append-only 2 h segment files, sealed segments immutable,
  7 d retention, batched fsync. This is the *readings* component of the replay bundle: it
  preserves the exact arrival order of samples interleaved with evaluation ticks, with a
  CRC per frame and a run-start frame marking each process restart (so replay resets the
  same empty rings live started with). Crash recovery truncates a torn tail at the last
  valid frame; a sealed segment is never clobbered.

It is pure-Go (`modernc.org/sqlite` elsewhere; plain 16-byte records here) — no CGO,
matching the cross-platform contract. The non-negotiable rule: **no statistics here, no disk
on the hot path, no external TSDB** — because identity is stamped at ingest and an external
store would break that.

### 4.3 `observe` — the three primitives and the fingerprint

Owning doc: [`docs/05`](docs/05-observation-and-fingerprint-pipeline.md). Package:
[`obsd/internal/observe`](obsd/internal/observe).

This is the system's *entire* statistical vocabulary — three operations, closed by
commitment:

1. **Threshold** (subtraction): is the latest value past its resolved bar? The result is a
   four-rung severity *ladder* — `Below | AtThreshold | Above | WellAbove` — oriented to the
   bar's violation direction (so it works identically for "above" bars like CPU and "below"
   bars like a memory-available floor). `AtThreshold` is the approach band (within 5% of the
   bar by default), which deliberately does **not** count as a crossing. `WellAbove` is the
   deep-violation line: when the bar is itself a fraction of a configured limit
   (e.g. bar = 0.8 × limit), well-above is the *raw limit itself*; otherwise it is a 1.10×
   fallback multiple. A NaN/±Inf value returns `Unknown`, never silently `Below` — because
   ordered comparisons against NaN are all false, and that would manufacture a clean state
   out of garbage.

2. **Rate-of-change** (differencing): the smoothed first difference of a counter over a
   window (default 5 min). It stable-sorts by timestamp (exporter timestamps regress),
   breaks the window on any gap larger than 2× the scrape interval (so a node briefly
   unreachable doesn't read as a clean low rate), and is *reset-aware* (sums only positive
   deltas; a counter decrease is a reset, recorded, never extrapolated across).

3. **Co-occurrence** (conjunction): several conditions true at once within a window. An
   empty member set returns `Unknown`, never spuriously all-met.

**Fingerprints** materialize one compact record per selected entity: the threshold ladder
per variable, rate summaries, co-occurrence window state — each stamped with the evaluation
time and the *bar source it used* (config | override | default-flagged), so every finding
carries its own evidence trail. Materialization is deterministic by construction: entities
sorted by CEI, components sorted by rule, clock injected. It evaluates only *usable*
bindings (bound + bar resolved + QA not failed) and only when exactly one stream exists for
the (entity, metric) pair — ambiguous evidence is never matched against an arbitrary bar.

A defense-in-depth finiteness guard drops any component whose arithmetic overflowed to
non-finite: the ingest gate stops bad *samples*, but arithmetic over finite inputs can still
overflow, and a non-finite component cannot be canonicalized for the digest.

**Why only three primitives.** Anything richer — a baseline, a seasonal model, a
distribution fit, an anomaly score — reintroduces per-customer learned state, the exact
thing the charter forbids. The cost of that refusal is a real blind spot (a novel failure
expressing itself only through un-thresholded signals), and the system patches that blind
spot honestly with the unexplained channel (§4.7) rather than papering over it with a
learned detector.

### 4.4 `binding` — compiling the ontology onto a live cluster

Owning doc: [`docs/04`](docs/04-binding-and-generalization-engine.md). Package:
[`obsd/internal/binding`](obsd/internal/binding).

Binding is the compiler between the type-level ontology and a specific cluster. It runs at
discovery time and on change, never on the hot path, and it is a *pure function* of its
inputs (ontology + inventory + customer config), so replay reproduces it byte-identically.
It does four things:

- **Equivalence-group resolution.** Whatever the customer's exporter calls a quantity maps
  to the canonical (OpenTelemetry) variable through authored regex variants, resolved once
  per stream name. Exactly one match = resolution; zero = outside the dialect bridge (not an
  error — the unexplained channel exists for this); more than one = ambiguous, surfaced as
  suspect, never silently picked.

- **Capability and distro gating** (`obtain.go`). Every authored signal is gated into
  `Obtainable | OutOfScopeUnobtainable | Indeterminate` based on what this cluster actually
  runs (detected from the workload corpus and kernel versions). A signal whose tool is
  absent is out-of-scope *with a reason*, not a failure; what the API cannot tell us is
  *indeterminate, stated*, never assumed. A distro gate doesn't remove a signal — it shifts
  its semantics and records that.

- **Per-instance threshold resolution** with strict precedence: customer config →
  operator override (vocabulary reserved, not yet built) → ontology default (always
  flagged). A config-relative bar reads the customer's own declared value × an authored
  factor. **When the customer declared nothing, the bar is `nil` — the entity is unbounded,
  Tier-B-ineligible, and listed as such; never silently defaulted.** The resolvability
  metric quantifies this honesty as `ConfigBound / ConfigEligible` — the declared-bar
  fill-rate — and both numerator and denominator are published, not just the ratio.

- **Semantic QA** (`semqa.go`): a name match is necessary but not sufficient, so every
  binding is *suspect by construction* until validated against real observed evidence.
  Checks run in order — platform variant, evidence existence, uniqueness (>1 stream →
  **failed**), scope (container vs pod vs node mismatch → **failed**), character (a `_total`
  counter behaving like a gauge, or decreasing more than once → **failed**), range sanity
  (negative or physically absurd values → **failed**) — and flip the verdict
  verified ↔ suspect ↔ failed in place. A failed binding keeps its record; the failure *is*
  the finding. The range bounds are derived from the cluster's own declared capacity
  (≈2× the largest node's allocatable), never learned.

Every (entity, variable) pair lands in exactly one of `bound | unresolved | out-of-scope`,
and the coverage report enumerates them — **hidden gaps are impossible by construction.**

### 4.5 `selection` — cost-bounded attention

Owning doc: [`docs/06`](docs/06-monitoring-selection-engine.md). Package:
[`obsd/internal/selection`](obsd/internal/selection) (named `selection`, not `select`,
because `select` is a Go keyword — the one documented doc↔code deviation).

Running clock inference on every gauge is infeasible, so attention is a funnel that entities
must *earn* from the graph. The deterministic core, `TierASet`, is a pure function of
(bindings, graph) — no inventory, no clock — so detection consumes it and replay reproduces
it. Two gates:

- **Gate 1 (coverage):** only fingerprint-eligible entities (bound + bar resolved + QA not
  failed) are candidates.
- **Gate 2 (phenomenon participation):** an entity earns attention iff its bound variables
  back a member signal of at least one phenomenon, *or* its metric backs a phenomenon's
  authored detection check. The union is deliberate — the matcher fires on check metrics, so
  the funnel must cover anything detection could fire on.

Three tiers result: **A** (detection — arithmetic, scales to the whole eligible cluster),
**B** (forecasting — model inference, deliberately scarce and budgeted at
`tier_b_budget_per_cycle = 50`, ranked precursor-first), and **none** (out-of-scope or
coverage-failing, *listed with a reason code*). Every entity gets exactly one selection
record with a reason — the operator can always answer "why is this watched?" — and Tier B
can never gate Tier A. Eligible-but-unbudgeted forecast targets are *published*, never
silently omitted.

### 4.6 `detect` — phenomenon matching, cascades, blast radius

Owning doc: [`docs/07`](docs/07-topological-detection-engine.md). Package:
[`obsd/internal/detect`](obsd/internal/detect).

This is the "now" half of the runtime. The ontology's phenomena *are* the detection units;
there is no separate playbook. A phenomenon lights up when its member signals reach their
threshold states in the *authored temporal pattern* (T0⁻ precursors, T0 event, T0⁺
consequence), with **required members mandatory and supporting members strengthening**.

The match is a conjunction: a required member that is observable-but-not-met fails the match
immediately; a required member that is *unobservable* (stale, absent, no check) is counted
as a named gap and the authored note explaining the missing member is carried so the "why"
of a degraded match is surfaced. Completeness = `RequiredMet / RequiredTotal`; a match is
`Full` only when nothing required was unobserved, else `Degraded` and marked with exactly
what was unobservable. **A single threshold crossing is never an alert** — only the
required-member conjunction surfaces.

**The traversal contract is the load-bearing trust mechanism.** First- and second-order
phenomena walk *only* the phenomenon's declared edge types, and **every traversed edge's
validity interval must overlap the co-occurrence window.** The verdict over an edge is
`Absent` (contributes nothing), `Suspect` (counts but degrades-and-names the match), or
`Valid`. An edge that has gone stale past its confirmation budget is suspect; an edge
absent during the window contributes nothing. This single rule prevents fabricating a 2-hop
correlation through a stale edge — described in the code as "the worst trust failure
available to the product." A spanned (multi-entity) phenomenon additionally requires
evidence *at its anchor*: without this, every container on a pressured node would falsely
light "throttling cascade" on the node's pressure signal alone. This anchor rule is what
lets the minimum-completeness floor sit at 0 while precision stays at 1.0.

**Cascade recognition** (`cascade.go`) uses authored `phenomenon_relation` edges two
strictly-descriptive ways. A downstream finding is paired with a trigger finding on a
topologically related entity, within a window — but only when the trigger *precedes or
coincides with* the downstream (a trigger never follows its effect), and only across a
genuinely *related* pair. The "why" shown is the authored relation note, verbatim. Purely
*corroborating* relations recognize no cascade (association, not direction). **Blast radius**
is the symmetric forward walk: which entities participate in a declared downstream
phenomenon and are topologically related now. One implementation serves both findings and
warnings, so a warning's radius can never disagree with a finding's.

The output is MEASURED with AUTHORED references attached — "these states co-occurred in this
authored pattern across these entities" — never fused into a causal sentence.

### 4.7 `unexplained` — the honestly-bounded blind-spot patch

Owning doc: [`docs/08`](docs/08-unexplained-anomaly-channel.md). Package:
[`obsd/internal/unexplained`](obsd/internal/unexplained).

This channel guarantees that loud activity matching *no* curated phenomenon is still
surfaced for a human — with no causal claim — and states plainly what even it cannot see.
"Loud" is defined precisely, without any learned baseline: an entity is loud iff at least
one signal is at `Above`/`WellAbove` (a bar crossing) *or* a rate-guarded signal exceeds its
authored guard. **Nothing else qualifies** — no distribution distance, no novelty score.

The residual blind spot is stated reflexively in the output: a signal carrying *neither* a
resolved bar *nor* a rate guard can never be loud, so a novel failure expressing itself only
through un-thresholded signals is invisible even here. This channel covers *known signals
exhibiting unknown patterns*, not unknown signals. Routing reconciles the loud set against
matches (a metric a phenomenon covered is subtracted), ages one card per persistent
unexplained condition (not a stream of repeats), and marks a card *superseded-by-match* when
authored knowledge later explains it. Recurrences aggregate by signature into
candidate-phenomenon reports for humans — the system proposes, it never authors.

### 4.8 `replay` — the determinism contract, mechanized

Owning doc: [`docs/05 M5`](docs/05-observation-and-fingerprint-pipeline.md),
[`docs/11`](docs/11-validation-and-calibration-harness.md). Package:
[`obsd/internal/replay`](obsd/internal/replay).

The capture side writes a bundle while obsd runs (readings, the resolved-bar set per
binding epoch, a manifest pinning the graph version + parameters + hot-ring capacity + edge
budgets). The engine side re-runs that bundle through the *real* observation and detection
components and reproduces every tick. The unit of the guarantee is the `TickResult` =
{fingerprints, findings, cascades, unexplained} — cascades and unexplained are included
because both are windowed over the tick sequence and must reproduce identically. Its digest
is a SHA-256 of the canonical JSON; the same function runs live (stamped into the tick
frame) and in replay (recomputed from disk), and **equality is the byte-identity guarantee.**

The engine refuses a graph whose version differs from the manifest, replays parameters from
the manifest (never the local file), walks under the *captured* edge budgets (suspicion is
budget-relative), resets reconstructed state at each run-start frame, and never "repairs" a
divergent tick — divergence is surfaced loudly, never patched. The forecast, cross-service,
and incident passes are explicitly off the digest (PROJECTED is non-deterministic by class;
only the deterministic join is graded).

A subtle but important capture rule: the bar set is content-hashed with timestamps zeroed,
so a periodic re-bind of an *unchanged* cluster mints no new epoch — without this, identical
bars produced spurious epoch churn.

---

## 5. The forecasting clock — the "next" engine

Owning doc: [`docs/09`](docs/09-forecasting-layer.md),
[`docs/27`](docs/27-forecast-trend-rescue.md). Off the digest, non-gating, opt-in (off by
default). The pipeline:
**eligibility → Tier-B budget → fetch → decompose → dynamics guard → regime-shift flag →
clock inference → band-calibrate → project against bar → guardrails → emit or silence.**

### 5.1 The clock contract — a semantics-free boundary

The model answers exactly one question: *when does this number cross that number.*
Everything that makes the answer mean something is authored graph knowledge, joined only at
surfacing. The contract is enforced at the proto layer
([`proto/vigil/clock/v1/clock.proto`](proto/vigil/clock/v1/clock.proto)): the service
carries **no string fields anywhere** — only float arrays, a horizon int, and quantile
arrays. The "model never sees labels, names, units, or graph structure" rule is thereby a
*compile-time* property, asserted by a conformance test that reflects over the generated
descriptor and fails if any string field is ever added. Consequence: any conformant model is
a drop-in, and swapping the clock touches *zero* knowledge.

The Go client ([`obsd/internal/clock`](obsd/internal/clock)) lazily dials clockd (startup
never blocks on an absent model), structurally validates the wire payload (a malformed band
is an error, never silently reshaped), and reports any failure as a *degraded* Tier-B
panel state — never as a perturbation of detection.

### 5.2 `clockd` — the model, swappable behind the contract

Package: [`clockd/src/clockd`](clockd/src/clockd) (Python/uv).

- **StubClock** (`forecast.py`): an honest pure-stdlib reference that proves the contract.
  Its point forecast holds the last level flat (never hallucinates a trend); its band is a
  random-walk widening, `quantile[q][h] = point[h] + z(q)·σ·√(h+1)`, where σ is the stddev
  of recent first-differences, floored at `1e-9` so **the band never collapses to a line
  even on a perfectly flat series.**
- **TimesFM 2.5** (`timesfm_clock.py`): the real model (`google/timesfm-2.5-200m`, pinned
  checkpoint + revision), max context 1024, max horizon 256. It runs with input
  normalization (scale-independence), quantile-crossing fixes, and a positive-inference
  prior. It emits decile quantiles and *linearly interpolates* a requested level between
  bracketing deciles — and **refuses** quantiles outside [0.1, 0.9] rather than
  extrapolating a fabricated tail.

The server (`server.py`) is pure transport; health is a numeric code on the wire (charter:
no strings), mapped to operator text on the Go side.

### 5.3 Eligibility and the Tier-B budget

`forecast/eligibility.go` is a static funnel over the bound graph producing the forecastable
target set plus a rejected list with verbatim reasons. The hard rejects are all honesty
gates: not bound, unbounded (no crossable bar), a rate-guard bar (not a crossable *level*),
a derived ratio (deferred), a counter shape (deferred), no stream. Crucially, each target
carries its **precursor** meaning — the phenomena for which this signal is a T0⁻ member —
which is what turns "a number crosses a number" into "early warning for a named
phenomenon." `selection/tierb.go` then ranks eligible targets *precursor-bearing first*,
applies the per-cycle ceiling, and publishes the unbudgeted remainder.

### 5.4 Footprint subtraction — forecasting the remainder, not the reason

`forecast/decompose.go` solves the sawtooth failure: on a ramp→OOM→reset history, a naïve
forecast projects the next *reset*, not the bar crossing. The fix is to splice the context
at the most recent confirmed event boundary and forecast only the clean post-event
remainder. A gauge *reset* (container restart) is confirmed only when **all three** hold:
a sharp relative drop, a magnitude floor over the observed range (so small wobble near zero
doesn't fire), and *persistence* (within 4 points the gauge does not recover to ≥90% of the
pre-drop level — a transient dip bounces back, a restart ramps from baseline). If the
remainder is too short or the explained fraction exceeds 0.9, forecasting it is untrustworthy
and the runner silences with a stated reason. On a clean series there is no reset, no splice,
and replay stays byte-identical. The footprint leaves the input; the reason stays in the
graph.

### 5.5 Regime-shift contamination — flag, never trim

`forecast/regimeshift.go` is the honest answer to the one case decomposition can't catch: an
*undeclared upward* baseline shift (a config change, a deploy) that raises the baseline
toward the bar, mixing two regimes and silently biasing the forecast. The detector finds the
largest upward jump (on robust medians, materiality relative to the *old* level), requires
*both* regimes to be roughly flat (a plateau guard), and — the cardinal rule — fires **only
on a step that plateaus, never on a ramp.** This matters because an upward ramp *is* the leak
signal the forecaster exists to find; auto-removing it would blind the early warning. So the
detector flags contamination as a MEASURED fact (the band may be inflated) but never trims
it. Only an operator-*declared* window cleans the input.

### 5.6 Trend rescue, calibration, projection

- **Dynamics guard + trend rescue** (`forecast/trend.go`): the low-variance silence gate
  (coefficient of variation below a floor) has a known blind spot — a series flat for 60
  points with a 4-point creep scores "flat" and gets silenced at the most valuable moment.
  The rescue is an EWMA-residual CUSUM (the *same* detector as the off-digest onset producer,
  §6.1) that confirms a sustained shift; the gate now silences as flat only if the series is
  flat *and* has no recent confirmed onset. A cold-start onset too short to forecast is
  *flagged* early/low-confidence (the band firms as the slope establishes), never silently
  dropped and never fabricated.
- **Band calibration** (`forecast/calibrate.go`): an affine scaling of the band half-width
  around the point forecast (the point is untouched — calibration shapes uncertainty, not the
  forecast). The factor is offline-derived from the backtest gate's measured band coverage,
  customer-invariant and versioned — the same class as any other declared default, explicitly
  *not* online learning.
- **Projection** (`forecast/project.go`): judges one clock answer against the resolved bar.
  It finds the first step the point crosses, then the band edges (when the upper band crosses
  vs the lower). The usefulness guardrail silences a band that is too wide — but judges width
  as a fraction of the *horizon* over the actionable near-cone (earliest→point), **never
  relative to time-to-cross** (dividing by time-to-cross inverted urgency and suppressed
  tight imminent bands exactly when they mattered most). A crossing whose far edge is beyond
  the horizon is reported as "may not happen," never clamped to false precision. The register
  is fixed: "projected to cross," never "will."

### 5.7 Churn-stable role-series

`forecast/roleseries.go` fixes the top real-world forecasting risk: a per-pod forecast dies
when the pod is replaced (HPA, rollout, OOM-restart). The fix is to forecast the durable
*workload role* (from the OwnerReference chain) as a per-bin aggregation of whichever member
pods exist in that bin. Empty bins are *omitted, not zero-filled* (honest absence, not a
measured zero). The reducer picks the member **closest to crossing** (max toward an "above"
bar, min toward "below") — explicitly *not* a sum, because Vigil's bars are per-entity and a
replica sum would cross a per-pod bar with two healthy pods. The churn-stable question is "is
the *worst* member about to cross its own bar." The aggregation is MEASURED arithmetic;
identity succession is AUTHORED (the OwnerReference).

---

## 6. The anomaly and causal lanes — off-digest inference

All of these are off the digest, firewalled, and exist to *surface candidates for human
judgment* — never to assert a cause. Their determinism is the narrower "same inputs + same
declared params ⇒ byte-identical output, re-run and diff."

### 6.1 `onset` — EWMA-residual CUSUM changepoint detection

Package: [`obsd/internal/onset`](obsd/internal/onset). Computes the deterministic *time* a
gauge stepped up or down — it annotates an already-loud signal with timing; it is not a
loudness or novelty score. The algorithm:

1. EWMA baseline: `base[i] = α·x[i] + (1−α)·base[i−1]`.
2. One-step-ahead residual: `resid[i] = x[i] − base[i−1]`.
3. Robust standardization: `z[i] = resid[i] / max(1.4826·MAD, 1e-9)` (MAD = median absolute
   deviation, floored so a flat prefix can't divide by zero).
4. **Two-sided CUSUM** (the load-bearing recursion):
   ```
   gp = max(0, gp + z[i] − K)      // upper accumulator
   gn = max(0, gn − z[i] − K)      // lower accumulator
   alarm when  gp > H  (up)   or   gn > H  (down)
   ```
5. **Sustained-shift confirmation:** backtrack to where the step began, compare the median
   *before* the step to the median *after* the alarm; keep the onset only if the realized
   shift is ≥ `MinZ` signal-sigmas in the alarm's direction. A transient blip settles back,
   so its pre/post medians are equal and it is filtered out.

Constants `α=0.10, K=0.5, H=4.0, Warmup=12, MinZ=3.0` are all operator-declared (K is the
classic CUSUM half-σ slack; H is the trip threshold; MinZ is the sustained-shift floor),
tuned by an in-package grid sweep for a low false-onset rate on stationary noise while
catching genuine 4–8σ steps — never fit to the live data.

**Why CUSUM and not ADWIN.** A live bake-off ([`docs/29`](docs/29-anomaly-detection-and-causal-inference.md),
harness `anomaly-bakeoff/`) compared this CUSUM against river's ADWIN and Page-Hinkley, with
`ruptures` Binseg as an offline oracle. ADWIN is a concept-drift detector — by design it
*suppresses* transient spikes (so "ADWIN for spike detection" is a category error) and,
decisively, it **lags a gentle creep by ~10×** (66 s vs 6 s), structurally, at every delta
setting. The creep is exactly what onset timing and forecast-activation exist to catch.
CUSUM is sharpest precisely where it matters. The swap was refuted on evidence; ADWIN's
narrow value would be at most a complementary regime-shift sensor.

### 6.2 `departure` — band departure (the anomaly of "my own forecast was wrong")

Package: [`obsd/internal/departure`](obsd/internal/departure). Flags a MEASURED sample that
fell *outside the PROJECTED band its own clock drew for that time* — "its own forecast did
not anticipate this." The math: `margin = MinExceedanceFraction · (Upper − Lower)`; fire if
`Realized > Upper + margin` or `Realized < Lower − margin`. The structural margin is a
*fraction of band width*, not a learned cutoff — **the band is the bar.** A noisy-but-stationary
series gets a wide band, so normal noise stays inside; the margin scales with the clock's own
uncertainty. A malformed or zero-width band is skipped, never used to fabricate a departure
(a zero-width band would make the fractional margin a one-ULP hair-trigger). It is classed
PROJECTED — the weakest input governs the class.

### 6.3 `assoc` — windowed Pearson dependency graph

Package: [`obsd/internal/assoc`](obsd/internal/assoc). Computes *undirected* MEASURED
`associated-with` edges between series. A correlation is a deterministic arithmetic
consequence of the readings (like a rate), so it is MEASURED, not learned — there is no
fitted weight, no optimization, no model. Series are binned (last value per bin), Pearson r
is computed over the overlapping bins in fixed order (reproducible float sum):
`r = Σ(xᵢ−x̄)(yᵢ−ȳ) / √(Σ(xᵢ−x̄)²·Σ(yᵢ−ȳ)²)`. An edge is kept iff the overlap ≥ 8 bins and
|r| ≥ 0.6 (both declared floors).

The edges are **deliberately symmetric** so they cannot be misread as a causal arrow. The
package also implements `PruneConfounders` — the first conditioning step of the PC
algorithm: edge a~b is removed if conditioning on a single neighbour c drops the partial
correlation `(r_ab − r_ac·r_bc)/√((1−r_ac²)(1−r_bc²))` below a declared floor. This only ever
*removes* edges, never adds or directs one — cutting confounded/transitive co-movers at the
source.

A `firewall_test.go` asserts no deterministic package imports `assoc`: routing a correlation
coefficient into forecast selection or footprint subtraction would launder a data-derived
number onto the replay-load-bearing path.

### 6.4 `cohypothesis` — direction-free co-onset hypotheses

Package: [`obsd/internal/cohypothesis`](obsd/internal/cohypothesis). For a pair of series
that is *both* associated and *both* registered an onset within a window (default 90 s), it
stages a **direction-free** candidate saying only "these two coupled series stepped
together" — never "A caused B." This is precisely the capability a competitor's product
*asserts* and Vigil's charter forbids it to assert: surface the lead, refuse the arrow. It
skips same-entity/same-pod pairs (two facets of one workload aren't a cross-workload lead),
carries the observed order as MEASURED *evidence* explicitly not a cause, and uses a strict
0.85 correlation floor (vs the dependency lane's 0.6) because a busy cluster has many weak
co-movers that would flood review. Candidate identity is content-keyed per *pair* (changing
onset times and coefficients live in the payload, not the id), so re-staging updates in place
rather than flooding. A TTL (15 min) plus a total cap reduced a real database from 1613
candidates to 40.

### 6.5 Offline causal discovery

`cohypothesis/discovery.go` ingests a shortlist produced by `harness/causal-discovery/causal.py`
(offline only, never graph writes): a fixed-lag enrichment over *pinned* candidate lags
(0,1,2,4 bins — never an argmax search, which would be a fitted parameter) followed by a
PCMCI+ conditional-independence prune (tigramite, ParCorr). PCMCI's directed edge is carried
*only* as a PROJECTED *hint*; a named operator authors the arrow. The proof-of-concept cut 26
cross-entity lag-0 edges to 6 directed candidates and pruned 7/7 spurious common-driver
edges. Its own conclusion — that auto-direction is unreliable at 15 s bins (propagation under
one bin reads as contemporaneous) — is *why* the system uses discovery to **prune, not to
assert.**

**The throughline:** association is symmetric because Pearson is symmetric; direction is
human-authored both by charter and on evidence; everything here is firewalled into a
`candidates.db` the deterministic path can never read.

---

## 7. The correlation and modality lanes — joining distinct sources

These lanes ingest *distinct MEASURED sources* and join them to the deterministic findings by
exact CEI — adjacently, never fused. The tokens cause/caused/root-cause appear nowhere in
their output. All are off the digest (a per-lane firewall test proves obsd is byte-identical
with each lane on or off).

### 7.1 `flow` — observed dependency edges and the chain reaction

Package: [`obsd/internal/flow`](obsd/internal/flow). Owning doc:
[`docs/15`](docs/15-cross-service-flow-lane.md).

A flow edge is *observed flow*, never "depends-on": it reconstructs workload→workload TCP
edges from Linux conntrack. The one load-bearing parse rule is DNAT recovery — the callee is
the *reply source* (the post-DNAT backend pod), not the ClusterIP VIP — which works
uniformly for VIP'd, DNAT'd, and direct calls. Cross-node calls SNAT-masked to the node
bridge are *unrecoverable from this node's table, so they are counted, never guessed*. The
edge type `flow` is package-local and deliberately absent from the production traversal-edge
registry, so it can never enter the replay digest.

On top sit three composed views:

- **Structural cascade** (digest-bearing core): walk *backward* from each degraded callee to
  its callers; the root is the inbound-clean degraded callee reaching the most impacted
  callers. Pure function of (store, degraded set, window).
- **Transitive root-cause chain** (`transitive.go`) — the headline "connect the chain
  reaction": stitch MEASURED-degraded workloads into an *ordered* chain over observed-flow
  edges, oriented **only** by the existence of the authored cross-service relation (direction
  is never inferred from timing — every fingerprint in a tick shares one evaluation time, so
  intra-tick onset carries no signal). The **cardinal no-merge rule**: one chain per
  connected degraded component; flow-disconnected coincident faults produce *separate* chains,
  never one merged chain — enforced by a gate that asserts independent faults yield zero
  chains. A non-degraded node on the path is a *silent intermediate*: traversed for
  connectivity but the chain is never bridged across it (a stated gap, never a false bridge).
- **Projected transitive cascade** (`projected_transitive.go`) — "forecast the ripple": from
  *one* forecast root (a callee projected to cross its own bar), walk callers, each inheriting
  the root's band **widened per hop** by `step = (LatestAt − EarliestAt)/2` (half the root
  band's own width, not an invented constant). The clock runs *once* at the root — re-running
  per hop would stack forecast error and manufacture causation. The band strictly widens on
  both sides with distance and never collapses (a beyond-horizon edge becomes Open, never a
  line). Band edges are stored as RFC3339 UTC precisely so string comparison is correct across
  the midnight boundary — a bug a `HH:MM` render would have hidden.

The single cross-service "why" is one curated `phenomenon_relation`
(callee-degradation → caller-impact), read from the released graph and surfaced verbatim. A
charter guard scans system-generated scaffolding for forbidden causal tokens but never
censors the curator's note (which legitimately says "propagates").

### 7.2 `incident` — deterministic identity across time

Package: [`obsd/internal/incident`](obsd/internal/incident). Joins a *phenomenon across
time* the way identity joins an *entity across restarts*. The grouping key is
**deterministic, not learned**: `sha256(phenomenonID ‖ roleCEIKey ‖ bucketStart)`. The graph
version is deliberately *absent* from the key (carried as an attribute) so a phenomenon
redefinition can't split a recurring incident. The time bucket floors an instant to a window
boundary against the Unix epoch, so boundaries are stable across processes. A gap larger than
the resolve horizon since last-seen means the condition resolved and re-fired
(`RecurrenceCount++`); a sub-horizon gap is the same episode. It is restart-invariant (no
wall-clock; every instant injected) and the gate asserts live memory equals the replay
verdict.

### 7.3 `events` / `eventdetect` — discrete Kubernetes events

Packages: [`obsd/internal/events`](obsd/internal/events),
[`obsd/internal/eventdetect`](obsd/internal/eventdetect). Closes a real blind spot: on kind,
an OOM emits *no* cAdvisor counter and often no "OOMKilled" Event — the fact lives only in
the container's `lastState.terminated` (reason `OOMKilled`, exit code 137), the authoritative
source. The lane reads both sources, bounded by a recency gate (a long-recovered OOM must not
surface as happening now). An event is MEASURED ("an OOMKilled event was emitted"); the
mapping of *which event reason corroborates which phenomenon* is AUTHORED (a curated
condition, surfaced verbatim). The join is exact-role-CEI equality — an event on role A can
never corroborate a gauge on role B. A standalone event with no corroborating gauge is
surfaced as a visible MEASURED finding, never auto-upgraded. When the graph authors an event
reason as a *required member*, `eventdetect` produces a degraded MEASURED phenomenon finding
with every other required member named as unobservable (degrade, never fabricate).

### 7.4 `trace` — the observed call graph

Package: [`obsd/internal/trace`](obsd/internal/trace). Turns OpenTelemetry spans into the
observed service call graph and proposes each discovered call as a *structural topology
candidate* for human promotion. A call edge is MEASURED ("A invoked B N times, p95 X ms, E
errors"); latency percentiles are computed by linear interpolation over *sorted* durations
(so the result is invariant to span arrival order). The parent→child arrow is observed
structure (who called whom), not cause. Critically, the lane measures latency but **never
derives a "slow" cutoff from the distribution** — a data-fit threshold would be a learned
quantity; latency is "slow" only against a declared SLO, else surfaced unbounded. Sampling
incompleteness (orphan spans) is counted, never hidden. Proposed topology edges use the
structural relation `topology` — the candidate store rejects a causal edge by construction.

### 7.5 `audit` — what changed just before this

Package: [`obsd/internal/audit`](obsd/internal/audit). Ingests the API-server audit log as
typed MEASURED change events and answers the first question every operator asks after an
incident — "what changed?" — without ever claiming the change caused it. It keeps only
completed, mutating, successful, named-object changes. The one load-bearing operation is the
**arrow-of-time prune**: a change is a candidate antecedent only if its *source* timestamp
(`stageTimestamp`, never async webhook delivery time) lies in `[onset − lookback, onset)`. A
change at or after onset cannot precede the incident and is pruned. This is a deterministic
filter over MEASURED timestamps, not an inference of cause. A surviving co-located change is
staged as a *direction-free* `observed-adjacency` hypothesis for human verification — the
ice-cream-and-drownings discipline: temporal adjacency is co-occurrence, never cause.

### 7.6 `logtmpl` — log-template mining

Package: [`obsd/internal/logtmpl`](obsd/internal/logtmpl). A Go-native Drain (fixed-depth
parse tree) that turns an unbounded log stream into a bounded set of (template, count) pairs
— a MEASURED arithmetic consequence of the byte stream. It is Go-native and *not* the Python
Drain3 library for one reason: Drain3's parse tree is input-order-dependent and must be
snapshotted to replay — the two determinism hazards the review flagged. The Go version sorts
its input first (a deterministic total order), so the template set is byte-identical and
invariant to arrival order. Regex is the authored first layer; this miner is the MEASURED
second layer for the unmapped tail; an LLM name for a template is a *proposed candidate*,
never an authored fact.

**The throughline:** these sources are sampled, unbounded, append-only, census-incomplete —
folding them into the deterministic tick would break replay. Each builds its own store or
rides a warm side-channel. Each clause is independently labelled, so the operator can trust
each part separately; honest partiality (SNAT-masked flows, orphan spans, silent
intermediates, role-unresolved entities) is always *counted*, never fabricated.

---

## 8. The propose→verify→promote spine — growth without corruption

The system must be able to *grow* its knowledge — new bindings, new edges, new phenomena —
without ever letting a machine-generated assertion enter the AUTHORED class. The answer is a
one-directional pipeline ([`docs/20`](docs/20-dynamic-graph-extension.md),
[`docs/12`](docs/12-graph-governance-and-release-engineering.md)):
**dgx proposes → candidate stages (firewalled) → governance verifies → a named human
authors the promotion.**

### 8.1 `candidate` — the firewalled staging store

Package: [`obsd/internal/candidate`](obsd/internal/candidate). Holds proposed nodes,
structural edges, members, bar sources, direction-free causal hypotheses, equivalence-group
mappings, and phenomenon candidates in a separate `candidates.db`. Two mechanical guarantees
keep it off the deterministic path: no detection/forecast package may import it (a
`go list -deps` test), and candidate overlays live in a subdirectory the graph loader
*structurally skips* (a test proves the release hash is byte-identical with and without it).

Provenance is *not* extended — the three classes stay immutable. A candidate carries an
*orthogonal* lifecycle status `{candidate, promoted, rejected, shadow}`: a stray metric's
readings are MEASURED, but its proposed join is merely `status=candidate`. Identity is
content-keyed: `sha256` over {kind, subject, relation, identity-payload, sorted evidence} —
deliberately *excluding* the model's prose rationale and the changing co-onset/PCMCI fields,
so the same logical proposal updates in place each cycle instead of flooding the queue.

The charter guard is structural: a `KindEdge` accepts *only* structural relations (topology,
associated-with, topo-adjacent); a causal proposal must use the direction-free
`KindCausalHypothesis`. The named-human gate, `Decide`, requires a mandatory `decidedBy` — "a
decision with no named human is refused; the harness can block but never approve." Even
promotion does not mutate the released graph; it produces a committable overlay artifact.

Two candidate kinds are special. `KindEquivGroup` collapses a whole stray *family* into one
promotion — the one promotion that actually moves MEASURED coverage — and deterministically
computes how many other strays a pattern would also capture (a count of facts, never a
confidence). `KindPhenomenonCandidate` aggregates a recurring unexplained anomaly by
signature into a phenomenon *skeleton* for a human to complete (members are corroborating by
construction — a candidate phenomenon implies no detection until a human authors one).
Ranking everywhere is a lexicographic tuple of integer *counts*, never a learned score; the
flood is bounded by a TTL plus a per-kind cap.

### 8.2 `dgx` — the agent harness (the model proposes, never writes)

Package: [`obsd/internal/dgx`](obsd/internal/dgx). The first place a model enters the loop.
An LLM proposes typed candidate extensions from read-only MEASURED context; the harness
authors nothing. The disciplines:

- **No write tool.** The provider returns strict JSON text; the harness parses it into typed
  proposals and stages survivors. The model never touches the store.
- **Grounding gate** (the anti-hallucination floor): every evidence reference a proposal
  cites must exist in the context the harness provided — a ref the model invented is an
  immediate reject. Tool-aware grounding stays honest: a ref is groundable only if the model
  actually *saw* it (a budget-truncated row is not citable; a failed tool launders no fact).
- **Evidence floor:** a proposal must cite at least a declared minimum number of facts.
- **Structural causal guard:** a causal edge is rejected (the same check the store applies),
  so a causal proposal can't even be staged.

The Provider is an interface — Groq (default, `llama-3.3-70b-versatile`), any
OpenAI-compatible endpoint (local models by base-URL swap), or a hermetic StaticProvider for
tests. Groq is one configuration, not a dependency. The harness is a *quality pre-filter for
the human queue*, never a validator of a claim — for a causal relation there is no machine
verifier, and promotion is justified solely by a named human.

### 8.3 `governance` — verification and AUTHORED integrity

Package: [`obsd/internal/governance`](obsd/internal/governance). A human-and-process layer
with tooling, exercised offline (it never runs on the hot path), reusing the *real* graph and
binding loaders so a classification or migration diff is computed over exactly the structures
the runtime consumes.

- **Change classes by blast radius:** `None < AdditiveLow < BehaviouralMedium <
  NormativeHigh`, with a release's class the *max* over its items. The rules are precise — a
  *new* flagged default is BehaviouralMedium (not yet relied upon), but *changing* a default,
  factor, or kind is NormativeHigh (it changes behaviour for every customer on the fallback).
  The conservative-up rule: where a change reads two ways, take the wider — under-classification
  is the release-blocking defect.
- **Regression gates** scale with the class: lints → binding-QA → falsification + replay-diff
  → shipped-class backtest. A required gate must have a *passed* result; a missing, skipped,
  or dry-run result blocks; a ledger run for a different proposal or a weaker class blocks ("a
  ledger does not transfer"). An unknown class triggers the full battery (fail safe).
- **The proposal workflow** runs authorship (empty author → block, this is where AUTHORED
  provenance starts), class verification against the authoritative diff, an evidence floor,
  and the regression gates. A clean mechanical pass yields *ReadyForReview* — the tooling can
  block but **never auto-approves.** A named human's decision is the only path to Approved.
- **Migration diffs, staged rollout, rollback, and a curation intake loop** complete the
  lifecycle: an upgrade is a re-bind (never a silent mutation); a stage with zero health
  signals checked is *unhealthy* ("nothing checked is not healthy"); a tripped signal
  re-pins and re-binds the prior release.

### 8.4 Why this shape

The agent is genuinely useful (it surfaces candidate joins and hypotheses a human would
miss) without ever being trusted. The firewall means even a fully-compromised model cannot
corrupt detection. The named-human gate means every AUTHORED assertion has an identity and
evidence behind it. This is how the graph grows by hundreds of authored facts across releases
v0.1.0 → v0.13.0 while the determinism and honesty guarantees hold unchanged.

---

## 9. The surfacing layer — where the classes meet

### 9.1 `api` — render and compose adjacently

Package: [`obsd/internal/api`](obsd/internal/api) (~4,500 lines). Owning doc:
[`docs/10`](docs/10-surfacing-and-operator-experience.md). This is the *one* place the three
provenance classes meet — each still wearing its label. It renders and composes; it never
produces findings or knowledge. The transport is plain `net/http` JSON over 30 routes under
`/api` (four of them POST: governance decide, validate-claim, causal-direction authoring,
context-window declaration). Each provider is a nilable snapshot func; a nil provider is the
honest "lane off" state, rendered as an explicit unavailable view, never an empty payload.

The adjacency discipline is visible in the view types: a `RootCauseChainView` is literally
classed `"MEASURED ⋈ AUTHORED (joined, never fused)"`; a `DepartureView` is
`"PROJECTED band ⋈ MEASURED sample"`. The "why" is *always* a separate verbatim AUTHORED note,
never inlined into a generated sentence. A `CharterViolations` linter substring-scans
serialized payloads against three denylists — generated causation, future certainty, class
fusion — as a *backstop*; the primary guarantee is that the surfacers generate no free causal
prose at all.

### 9.2 `mcp` — the read-only synthesis relay

Package: [`obsd/internal/mcp`](obsd/internal/mcp). Owning doc:
[`docs/23`](docs/23-mcp-operator-parity.md). Exposes Vigil's already-classed surfaces to an
LLM client over JSON-RPC, read-only — it imports only `internal/api` and the standard
library, so no-write-back is *structural* (there is no writer in scope). Its marquee tool is
the silence ledger: a provable negative — "what are we *not* watching, and why" — exactly
what an LLM hallucinates and Vigil can state with certainty.

The key design move: the charter's enforcement point *moves* from input-withholding to
**output-labeling.** Vigil emits classed facts; the AI synthesizes a cause and remediation
from them; the synthesis is disciplined at the boundary. `validate_claim` is an
honest-labeler that **never blocks** (the cardinal rule is false-block == 0): a claim
matching an authored relation is surfaced *as authored*; a claim with no authored basis is
flagged as the AI's hypothesis. `emit_advisory` *refuses* a fabricated cause outright and
withholds even a clean advisory until its backtest gate passes — so a hypothesis can never
wear a MEASURED/PROJECTED/AUTHORED badge. ADVISORY is the gated fourth class.

### 9.3 `notify` — the off-digest alert lane

Package: [`obsd/internal/notify`](obsd/internal/notify). Owning doc:
[`docs/30`](docs/30-alerting-lane.md). Emails classed facts the surfacing layer already
published, firewalled from the deterministic path like `candidate`. It is a *dumb transport*:
it does not re-derive a class, paraphrase, or emit a "why" of its own. The fatigue controls
are all operator-declared constants (never fitted), applied in order: quiet hours (holds, not
loses), per-key cooldown (edge-trigger + flap damper, durable across restart), a token-bucket
rate limit (refunded on send failure), and coalescing (one digest email per tick). The
register is enforced: a PROJECTED clause reads "projected to cross," never "will"; a
system-generated headline never contains a causal token; the authored note is the only place
a curator's wording appears. Default-off means the deterministic path is byte-identical
without it.

### 9.4 `console` — the operator frontend

Directory: [`console`](console) (Vite + React 19 + TypeScript + Tailwind + Cytoscape). A thin
typed client over `/api` and `/mcp` with 14 surfaces (insights, events, root-cause chain,
early-warnings, anomaly inbox, incidents, timeline, coverage, silence ledger, referee,
config, MCP, causal hypotheses, governance). The design system carries the three provenance
classes as the *one shared design token* — provenance is conveyed by form, not just hue — and
every off/gate-pending lane renders its honest note rather than an empty panel.

---

## 10. The ontology graph — the curated knowledge

Directory: [`ontology`](ontology). Owning docs:
[`docs/02`](docs/02-ontology-graph-specification.md),
[`docs/12`](docs/12-graph-governance-and-release-engineering.md).

The base graph ([`ontology/graph/k8s_signal_kg.json`](ontology/graph/k8s_signal_kg.json),
~1.5 MB) is an immutable vendored mirror: **842 nodes, 3,759 edges.**

| Node type | Count | | Edge type | Count |
|---|---|---|---|---|
| **Signal** | **589** | | emitted_by | 817 |
| Entity | 47 | | requires_capability | 675 |
| CapabilityPrereq | 42 | | attaches_to / has_modality | 589 each |
| Tool | 38 | | has_gotcha | 472 |
| **CorrelationGroup** (= the 38 **phenomena**) | **38** | | participates_in | 180 |
| EquivalenceGroup | 35 | | derived_from | 142 |
| DistroVersionGate | 19 | | **phenomenon_relation** (authored causal links) | **12** |

A phenomenon *is* a CorrelationGroup (id-prefixed `PHEN_`); there is no separate type. Note
the proportions: 589 signals catalogued, but only 12 authored causal relationships — the
graph is deliberately rich in *what can be measured* and conservative in *what is asserted to
relate.*

**Overlays** ([`ontology/graph/overlays`](ontology/graph/overlays), 20 in the production
glob) add authored knowledge, never redefine the base:
`threshold-rules-v1…v6` (config-relative bars and flagged defaults),
`detect-conditions-v1…v7` (member→fingerprint checks, entity-local → first-order → two-hop →
KSM → eviction → disk → PVC), `cross-service-v0` (the cross-service relation),
`disk-filling-v1`, `phenomenon-severity-v1` (ranking-only severity on all 38),
`init-container-failure-v1`, `detection-status-v1`, `corrections-v1` (the governed overrides
channel). A `candidate/` subdirectory (firewalled, never in the release hash) and an
`experimental/` subdirectory (flag-gated lanes) sit outside the production set.

**Releases** ([`ontology/releases`](ontology/releases)) are SHA-256-pinned manifests. The
loader hashes the base bytes plus each overlay's raw bytes in sorted order, so *a changed
overlay is a changed release.* `VerifyRelease` enforces immutability; the system
self-identifies its running release by hash at startup. History: v0.1.0 (base) → first-order
→ two-hop/cascade → cross-service → four KSM batches (v0.5–v0.8) → disk-filling → severity →
init-container → membership-structuring lint gate → **v0.13.0** (corrections channel,
current). [`tools/graphlint`](tools/graphlint) validates every YAML against the schema and
enforces the membership-structuring and release-immutability gates; it runs green.

---

## 11. The validation harness — falsification as a gate

Directory: [`harness`](harness) (Python/uv). Owning doc:
[`docs/11`](docs/11-validation-and-calibration-harness.md). Each gate is a *falsification
test*: it states a claim the system makes and fails the build if the claim is false. The
structural invariant across all of them: **INSUFFICIENT is never a pass** — a thin corpus
returns non-zero, so absence of evidence can never be laundered into certification.

| Gate | The claim it falsifies |
|---|---|
| `forecast_gate` | A forecast class is mis-calibrated (band coverage out of range, crossing recall < 1.0, false rate too high). |
| `governance` | A release shipped without its required regression gates passing. The harness only blocks. |
| `event_detection_gate` | Event-driven detection is unfaithful (wrong finding, false upgrade, phantom/missing cascade, non-verbatim note, or the digest changed). |
| `events_corroboration_gate` | The discrete-event join is identity-wrong (event on role A corroborated against gauge on role B). |
| `crossservice_gate` | The MEASURED backward cascade names the wrong root or over-fires (recall = precision = 1.0 required). |
| `projected_crossservice_gate` | The forecast-seeded cascade doesn't lead reality, or false-anticipates on a negative scenario. |
| `departure_gate` | The band-departure producer false-fires on a stationary/wide-band/decoy series (cardinal zero). |
| `incident_memory_gate` | Cross-run incident identity isn't the deterministic `sha256(phenomenon‖role‖bucket)` (fragments, inflates, or differs after restart). |
| `validate_claim_gate` | The referee blocks truth (cardinal false-block == 0) or misses fabrications (recall ≥ 0.90). |
| `mcp_gate` | The read-only MCP is non-deterministic or leaks content while refused. |
| `app_slo_gate` | An app phenomenon fires without a customer-declared SLO (no fabricated bar). |
| `sensitivity` | The shipped detection-sensitivity defaults aren't the calibrated optimum. |
| `backtest` | The band-coverage / time-to-cross primitives are unsound. |
| `transitive_chain_gate` | The MEASURED chain invents or merges structure (cardinal: no chain holds both members of an independent pair). |
| `projected_transitive_gate` | The multi-hop projected ripple tightens uncertainty downstream, or has > 1 forecast root. |
| `test_replay_determinism` | Replay isn't reproducible byte-for-byte. |

Plus an `anomaly-bakeoff` (CUSUM vs ADWIN vs Page-Hinkley, §6.1) and an offline
`causal-discovery` harness (§6.5). The [`justfile`](justfile) wires each as a one-line recipe
(`just <name>-gate`, exit 0 = passed); `just ci` runs lint + gen-check + the race-enabled test
suite as the standing gate.

---

## 12. The orchestrator — the tick loop

File: [`obsd/cmd/obsd/main.go`](obsd/cmd/obsd/main.go) (~3,700 lines). It parses ~40
`--*-enabled` flags (each off by default, each documented "off = byte-identical"), loads the
pinned ontology release, self-identifies it by hash, and runs the loops.

**The store gate is the load-bearing invariant.** A single `sync.RWMutex` guards the store.
The scrape loop fetches over the network *outside* the lock, then takes the write lock only to
ingest a whole cycle atomically; the eval loop takes the read lock for an entire tick. The
consequence: **every evaluation tick observes complete scrape cycles, never a half-ingested
one** — the visibility property replay depends on. All in-digest lanes (cAdvisor,
node-exporter, app-metrics, KSM) append into the same gated cycle.

**The eval tick** (the digest-bearing core) builds one canonical topology snapshot, rebuilds
an edge store from it (so *recorded equals evaluated*), compiles bindings, runs
selection → fingerprints → detect → cascades → unexplained. Those five outputs *are* the
digest.

**Off-digest lanes attach two ways without perturbing the digest:**
- *Surface-only join:* event-driven detection builds *copies* of findings/cascades with event
  findings appended; the originals (used by unexplained routing and capture) are never
  mutated, so replay stays byte-identical.
- *Goroutine + atomic snapshot:* every warm producer (association, onset, co-hypothesis,
  causal-discovery, logs, audit, traces, phenomenon candidates, the DGX agent, alerts, the
  flow and event collectors) runs as its own goroutine and publishes through an
  `atomic.Pointer[…View]` that the API reads race-free. The forecast lane is the same: the eval
  tick freezes its Tier-B targets into an atomic pointer once per tick, and the forecast loop
  only *reads* that snapshot — so forecasting sees exactly one tick's bindings and can never
  perturb detection.

The inventory loop also renders the live entity inventory and the Phase-0a mis-join exit gate
every tick (a mis-join logs an error). The health server binds `/metrics`, `/healthz`,
`/readyz`, the `/api` surface, and (under `--mcp-enabled`) `/mcp` — synchronously, so a bind
failure fails loudly rather than running gate-blind.

---

## 13. Cross-cutting invariants

| Invariant | Mechanism | Where |
|---|---|---|
| Three classes, joined never fused | classes are types; the join lives only in `api`, composed adjacently with a labelled `Class` glyph | `api/*.go`, `docs/01` |
| No invented causation | causal direction only ever AUTHORED; inference lanes emit no causal tokens; `KindEdge` rejects causal relations | `flow`, `audit`, `cohypothesis`, `candidate/store.go` |
| No invented thresholds | borrowed normativity: config → override → flagged default; unbounded series listed, never defaulted | `binding/compile.go` |
| No learned per-customer state | three primitives only; every constant operator-declared; rankings are count tuples | `observe`, `params/defaults.dev.yaml` |
| Determinism by construction | per-tick SHA-256 digest; live-stamped, replay-recomputed; equality is the guarantee | `replay/digest.go` |
| Non-gating | detection byte-identical with every off-digest lane on/off/degraded | per-lane `firewall_test.go` |
| Firewall | `go list -deps` import tests + an overlay subdir the loader skips | `*/firewall_test.go`, `graph/overlay_firewall_test.go` |
| Honest partial coverage | every gap counted and surfaced (quarantines, unbounded, SNAT-masked, orphan spans, silent intermediates, the silence ledger) | throughout |
| Human-authored growth | named-human `Decide`; harness blocks but never approves | `candidate/store.go`, `governance/proposal.go` |
| Bands never collapse | stub σ floor; affine-around-point calibration; beyond-horizon → Open, never a line | `clockd/forecast.py`, `forecast/calibrate.go`, `forecast/project.go` |

---

## 14. Why this design suits the environment

Kubernetes operations is a domain of **high churn, heterogeneous telemetry, and
high-consequence decisions made under time pressure.** Each major design choice is a direct
response:

- **Determinism by construction** answers the trust problem. Under incident pressure, an
  operator needs to know that a finding is real and reproducible, not the output of a model
  that drifted. A byte-replayable engine with no learned state in the loop gives exactly that:
  any finding can be reproduced from pinned inputs, and any divergence is loud.

- **Borrowed normativity** answers the threshold problem. There is no universal "correct" CPU
  or memory bar — it depends on the customer's own declared limits and SLOs. Inventing one
  would be guessing; learning one would be hidden per-customer state. Reading the customer's
  own declarations, and honestly listing what they didn't declare, is the only stance that
  scales across clusters without lying.

- **The off-digest firewall** answers the integration problem. Real root-cause signal lives in
  sampled, unbounded, census-incomplete sources (events, traces, audit, logs, flow). Vigil
  ingests them — but never lets their non-determinism touch the replayable core. New lanes are
  *additive*: deploy an exporter, author the join, and the lane lights up without core surgery,
  because the architecture has two clean seams (borrowed normativity for bars, the off-digest
  events lane for sampled inputs).

- **Human-authored causation** answers the correctness problem. The hard empirical lesson —
  reproduced in the bake-off and the causal-discovery proof-of-concept — is that auto-discovered
  causal direction is unreliable (confounders, sub-resolution lead-lag). The honest move is to
  use inference to *prune and rank* candidates and let a named human author the arrow. This is
  slower than a product that confidently asserts causes, and it is right far more often.

- **Cost-bounded attention and the narrow clock** answer the scale problem. Detection is pure
  arithmetic and scales to the whole eligible cluster; forecasting is model inference, so it is
  budgeted and ranked precursor-first, with the overflow published. The clock sees bare floats
  only, so the expensive model is fully swappable and never entangled with knowledge. Identity
  anchored on the durable role survives the churn (rollouts, HPA, OOM-restarts) that breaks
  per-pod approaches.

The cumulative effect is a system that is honest about its edges and therefore trustworthy
inside them — the property that lets it scale as an operational intelligence layer rather than
another alert firehose.

---

## 15. Honest boundaries — what Vigil deliberately cannot do

Stated plainly, because the architecture states them (the `get_blindspots` surface, the
silence ledger, and the per-lane coverage censuses make these first-class):

- **Data correctness.** Vigil sees that a number crossed a bar; it cannot see that the number
  is *wrong* (a mis-configured exporter, a semantically broken metric) beyond the range/character
  QA checks.
- **Business semantics.** It detects technical phenomena, not "this checkout flow is losing
  money," except insofar as a customer declares an SLO it can measure against.
- **Unauthored causal direction.** By design — direction is human-authored. An un-curated causal
  relationship is invisible as a *cause* (though its co-occurrence may surface as a hypothesis).
- **Novel failures through un-thresholded signals.** The unexplained channel covers known
  signals in unknown patterns; a failure expressing itself only through a signal carrying neither
  a bar nor a rate guard is invisible even there — stated in the channel's own output.
- **Sources it doesn't ingest.** Off-CPU attribution, deep profiling, full L7 semantics beyond
  the trace lane, and pre-SNAT cross-node flow callers are counted gaps, not silent ones.

None of these is hidden. Each is the deliberate cost of refusing to fabricate, and each is
surfaced rather than implied away — which is, in the end, the whole point of the system.

---

## 16. Appendix — package index

Deterministic spine (in the digest):

| Package | Role | Owning doc |
|---|---|---|
| [`identity`](obsd/internal/identity) | CEI minting, normalization, lifecycle, mis-join audit | 03 |
| [`qss`](obsd/internal/qss) | hot ring + warm segments time-series store | 05 |
| [`observe`](obsd/internal/observe) | three primitives + fingerprint | 05 |
| [`binding`](obsd/internal/binding) | ontology→cluster compile, bar resolution, semantic QA | 04 |
| [`selection`](obsd/internal/selection) | cost-bounded attention funnel, tiers, reasons | 06 |
| [`detect`](obsd/internal/detect) | phenomenon matching, cascade, blast radius | 07 |
| [`unexplained`](obsd/internal/unexplained) | loud-but-unmatched channel + stated blind spot | 08 |
| [`replay`](obsd/internal/replay) | capture + byte-identical replay digest | 05, 11 |
| [`graph`](obsd/internal/graph) | loader, release/version hashing, overlays, firewall | 02, 12 |
| [`params`](obsd/internal/params) | declared parameters (embedded, tested) | 14 |

Forecasting (off-digest, non-gating):

| Package | Role | Owning doc |
|---|---|---|
| [`clock`](obsd/internal/clock) | Go client for the semantics-free clock contract | 09 |
| [`forecast`](obsd/internal/forecast) | eligibility, decompose, regime-shift, trend, calibrate, project, role-series | 09, 27 |
| [`clockd`](clockd) | TimesFM 2.5 / stub clock (Python) | 09 |

Inference lanes (off-digest, firewalled):

| Package | Role | Owning doc |
|---|---|---|
| [`onset`](obsd/internal/onset) | EWMA-residual CUSUM changepoint timing | 29 |
| [`departure`](obsd/internal/departure) | band-departure anomaly | 15 |
| [`assoc`](obsd/internal/assoc) | windowed Pearson dependency graph + PC prune | 20 |
| [`cohypothesis`](obsd/internal/cohypothesis) | direction-free co-onset hypotheses + discovery ingest | 22, 29 |

Correlation / modality lanes (off-digest):

| Package | Role | Owning doc |
|---|---|---|
| [`flow`](obsd/internal/flow) | conntrack flow edges, transitive + projected cascades | 15 |
| [`incident`](obsd/internal/incident) | deterministic cross-run incident identity | 25 |
| [`events`](obsd/internal/events) / [`eventdetect`](obsd/internal/eventdetect) | K8s events + corroboration join | 07 (G1) |
| [`trace`](obsd/internal/trace) | OTel spans → observed call graph + topology candidates | 20 |
| [`audit`](obsd/internal/audit) | apiserver audit log + arrow-of-time prune | 20 |
| [`logtmpl`](obsd/internal/logtmpl) | Go-native Drain log-template mining | 20 |

Propose→verify→promote + surfacing:

| Package | Role | Owning doc |
|---|---|---|
| [`candidate`](obsd/internal/candidate) | firewalled staging store, content-keyed, lifecycle status | 20 |
| [`dgx`](obsd/internal/dgx) | LLM agent harness (proposes, never writes) | 20, 21 |
| [`governance`](obsd/internal/governance) | change classes, regression gates, rollout, intake | 12 |
| [`api`](obsd/internal/api) | surfacing back end, adjacency join, charter linter | 10 |
| [`mcp`](obsd/internal/mcp) | read-only synthesis relay, validate-claim, advisory | 23 |
| [`notify`](obsd/internal/notify) | off-digest alert lane, fatigue controls | 30 |
| [`store`](obsd/internal/store) | surfacing-side SQLite persistence | 14 |
| [`kube`](obsd/internal/kube) | Kubernetes client / informer plumbing | — |

Supporting: [`proto`](proto) (clock contract), [`ontology`](ontology) (the graph),
[`harness`](harness) (falsification gates), [`console`](console) (operator UI),
[`tools/graphlint`](tools/graphlint) (ontology validation), [`deploy`](deploy) (kind +
RBAC), [`simulator`](simulator) (chaos/failure simulator).

---

*This index reflects the system as built on branch `final`. For the cross-platform build
contract and per-package charters, see [`DEVELOPMENT.md`](DEVELOPMENT.md) and each package's
`doc.go`; for design rationale, the numbered suite in [`docs/`](docs/); for current status,
[`artifacts/task.md`](artifacts/task.md).*
