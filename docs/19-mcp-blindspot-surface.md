# 19 — MCP Blindspot Surface

**Status:** PROPOSED (design plan). The substrate this builds on — the silence
ledger, the coverage report, the obtainability gate, and the read-only MCP server —
is BUILT and live (citations below). The **blindspot registry tool** described in §6
is the one new, unbuilt component; everything else assembles existing, classed
outputs.

**Role in suite:** The MCP-boundary extension of the charter's *honest partial
coverage* commitment (doc 01). Vigil already states absence to a human operator
(the Coverage Report, the Silence Ledger). This doc plans the same statement of
absence delivered to an **MCP-connected AI synthesizer** as a first-class, queryable
INPUT — so the AI reasons *with* Vigil's blind spots instead of papering over them.

> Owning context: charter [`docs/01-epistemic-separation-charter.md`](01-epistemic-separation-charter.md);
> the synthesis relay design in `obsd/internal/mcp/server.go`; the harm × reachability
> assessment in the coverage-harm audit (branch v4, 2026-06-17). Every concrete
> phenomenon and limit below is grounded in a file:line read or the audit; PROPOSED
> tags mark anything not yet built.

---

## 1. Why — an honest synthesizer must know what it cannot see

The charter (doc 01 §3) types coverage and honesty statements as a corollary of
MEASURED: binding states, match completeness, and bar resolvability "are facts about
the system's own visibility and are surfaced with the same rigor as facts about the
cluster." Vigil is, structurally, *good at stating a provable negative* — and an LLM
is structurally *bad* at it. The silence ledger says so in its own contract:

> "An LLM is structurally bad at one thing Vigil is structurally good at: stating a
> provable NEGATIVE. Ask a model 'what are we NOT watching, and why' and it will
> confabulate a plausible answer; ask Vigil and it can enumerate every (entity,
> variable) pair … and say, for each, whether it is WATCHED or SILENT and the exact
> reason." — `obsd/internal/api/silence.go:12-17`

When an MCP-connected AI synthesizes a cause or a remediation from Vigil's classed
facts, the failure mode is **over-claim by omission**: the AI sees a degraded
front-end and a healthy-looking dependency graph, and asserts a MEASURED cause that
is really an **unobserved modality** Vigil never had a signal for (a DNS failure, an
expiring cert, a slow etcd). Vigil cannot observe these today — and silence, to a
model, reads as *absence of the problem* rather than *absence of the sensor*.

The blindspot surface closes that gap. It turns honest partial coverage from a
passive disclosure an operator might read into an **active hint the AI must consult**
before committing to a cause. The discipline is exactly the charter's: *state the
absence, never fabricate the phenomenon.*

---

## 2. Taxonomy of blind spots

Four categories, by *kind of blindness*. The first two are recoverable with
engineering (more ingest); the third is blind by design; the fourth is a property of
*this* cluster, not of Vigil. The category determines what the AI should do with the
hint — recommend an out-of-band check, treat as a permanent epistemic floor, or note
an environment limit.

### 2(a) UNWIRED-BUT-REACHABLE — could be wired with more ingest

A known phenomenon whose signal lane *could* exist on a real cluster but is not yet
ingested or not yet authored. The blocker is engineering effort, not physics. Harm ×
reachability counts and blockers are from the coverage-harm audit (v4, 2026-06-17).

| Phenomenon | Harm / reach | Blocker (what is missing) |
|---|---|---|
| `NODE_NOT_READY` | critical-now | node-condition `Ready` derivation — "same machinery as DISK, a trivial follow-up now" (audit) |
| `SCHEDULING_FAILURE` | reachable | events-lane (`FailedScheduling`) reachable, but `involved_kind` handling differs; or a scheduler scrape (audit) |
| `INIT_CONTAINER_FAILURE` | events-reachable, bigger | **zero base KG members** — needs an authored member first, then the events lane (audit) |
| `POD_STUCK_TERMINATING` | events/derivative | reachable via events / pod-status derivation; not yet authored |
| `PDB_VIOLATION` | deferred | **zero CEI mapping + zero members** — deferred, feasibility unverified (audit) |
| `PLEG_HANGS`, `CRI_ERRORS`, `RUNTIME_IO_STALL`, `KUBELET_API_CONNECTION` | HIGH | p99 **histograms** needing bucket-interpolation ingest (doc-02 §8 `data_type` queue), NOT scalar-ingestable today; the G3 kubelet-main `/metrics` lane (audit). See §2(c). |

Audit conclusion worth surfacing verbatim to the AI: **no CRITICAL phenomenon is
gated *only* by the G3 kubelet-main lane** — "it unlocks no CRITICAL, and its HIGH
targets need a separate histogram lane" (audit). So the AI should not assume the
histogram targets are "almost wired."

### 2(b) MODALITY-ABSENT — no signal exists today, at all

A class of failure for which Vigil ingests **no series on any current lane**. These
are the genuine dangerous blind spots: the symptom (a degraded caller) is visible,
but the cause modality is invisible, so an unguarded AI will mis-attribute the cause
to whatever it *can* see.

| Phenomenon | Why blind | What ingest would unblock it |
|---|---|---|
| `CERT_EXPIRY` | no cert-expiry series scraped | apiserver / control-plane metrics (RBAC + control-plane endpoints) (audit) |
| `DNS_FAILURE` | no CoreDNS / resolver series | CoreDNS / CNI metrics (audit) |
| `ETCD_SLOW_PATH` | no etcd latency series | etcd metrics endpoint (`--listen-metrics-urls` is a config flag → `CAP_ETCD_METRICS_EXPOSED` is *presence-necessary*, `obsd/internal/binding/obtain.go:203`) |
| `CNI_FAILURE` | no CNI series; **feasibility unverified** | CNI / dataplane metrics — flagged by audit, not confirmed reachable |

These "need apiserver/etcd/scheduler scrape (RBAC + control-plane endpoints) or
CNI/CoreDNS metrics" (audit) — **a separate ingestion *modality*, not a relay tweak.**
This is the most important category for the AI to know about: a symptom "consistent
with DNS_FAILURE" must trigger a *recommend-an-out-of-band-check* response, never a
MEASURED assertion (worked example in §4).

### 2(c) EPISTEMIC-FLOOR — blind by design, permanently

Limits the architecture *chooses*. No amount of ingest removes them; they follow from
the charter. The AI must treat these as hard ceilings on what any Vigil fact can
support.

| Floor | What Vigil sees / cannot see | Charter basis |
|---|---|---|
| **Data-CORRECTNESS** | Vigil sees a **bar-crossing**, not whether the bar or the underlying value is *correct*. A wrong-but-unbreached value is invisible. | doc 01 §3 — coverage/honesty is MEASURED about *visibility*, not value-truth |
| **Causal-DIRECTION (unauthored)** | A topological match is a **co-occurrence**, never a proof of cause; a cascade is "the graph's authored trigger→downstream edge lighting up … nothing stronger" (doc 01:35-36). Direction not in the authored graph is unprovable. | doc 01 §4 prohibited derivations |
| **Missing APPLICATION / BUSINESS context** | Vigil has no notion of revenue, SLA tier, user impact, or intent beyond declared SLOs. The remediation playbook explicitly leaves this to the AI: "The remediation is YOURS to reason from ops knowledge + application context you gather" (`obsd/internal/mcp/server.go:184`). | charter scope — no learned or business semantics |
| **Unexplained channel's residual blind spot** | A signal carrying **neither a resolved bar nor an authored rate guard can never be loud**, so "a novel failure expressing itself only through un-thresholded, un-guarded signals is invisible even to this channel" (doc 08:28-30). The channel covers *known signals exhibiting unknown patterns, not unknown signals.* | doc 08 §3.2 |

### 2(d) ENV-INDUCED — blind on *this* cluster only

A signal Vigil *would* obtain on a different cluster but cannot on the current one,
for a stated environment reason. These are **hardness facts for a cluster type, not
design gaps** (see Caveats) — and the obtainability gate records each as
`out-of-scope` or `indeterminate` *with the reason*, never silently absent
(`obsd/internal/binding/obtain.go:38-45`).

| Blind spot (on kind) | Why | Obtainability verdict |
|---|---|---|
| **PVC FILL** | local-path storageClass emits **zero `kubelet_volume_stats`** | bound bar with no scraped stream → silence class `no-stream-key` (the "PVC dark-bar", `obsd/internal/api/silence.go:37`). Live-verifiable on a CSI cluster. |
| **OOM via cAdvisor counter** | `container_oom_events_total = 0` on kind | OOM is seen via the **events lane + pod `lastState.terminated`**, not the counter (audit). The counter path is dark; the events path is lit. |
| **PSI memory/IO pressure** | needs **kernel ≥ 4.20** | `CAP_KERNEL_GE_4_20`; if unmet → `out-of-scope`; on **mixed kernels** → `indeterminate`, stated ("met on sampled node but kernel versions differ", `obsd/internal/binding/obtain.go:174-176, 221-223`) |
| **Node-level pressure realism** | 3 kind nodes share one host kernel / one Docker VM disk | node-pressure corpus is not authoritative until a standalone cluster exists (doc 14 §3.1 honest caveat) — a *confidence* limit, surfaced as a caveat, not a hard silence |

---

## 3. How each blind spot is surfaced to the MCP AI

The surface is a **structured "what I cannot see" manifest** plus **per-query
relevant-blindspot hints**, assembled from outputs that already exist and are already
provenance-classed. Two existing tools already deliver most of it.

### 3.1 The substrate that already exists

- **`get_silence_ledger`** — the deterministic ABSENCE ledger. Every
  (entity, variable) pair Vigil produced is WATCHED or SILENT with an exact reason
  from the exhaustive vocabulary: `unbounded` / `no-stream-key` / `unresolved` /
  `out-of-scope` (`obsd/internal/api/silence.go:30-45`). Completeness is guaranteed by
  construction — `TotalPairs == Watched + Silent == len(res.Bindings)`, so no pair can
  be silently dropped (`silence.go:23-27`). The tool description tells the AI exactly
  this: "Use this to state a provable negative — what is NOT being watched and why —
  instead of guessing" (`obsd/internal/mcp/server.go:197`).
- **`get_coverage`** — the Coverage Report: per-phenomenon observability
  (`full | partial | none`), per-rule binding coverage, the unbounded list, and QA
  status. The report **sorts gaps first** — "surface the gaps: none, then partial,
  then full" (`obsd/internal/api/coverage.go:132-139`) — so the blind phenomena rank at
  the top of the payload the AI reads. `PhenomenonRow.MissingReasons` carries the
  per-phenomenon member gaps (`coverage.go:53`), and `CoverageView.Caveats` carries
  the standing honesty notes sourced from `CoverageReport.Notes`
  (`coverage.go:26, 109`).
- **`ObservabilityReport`** — only `role=required` members decide observability;
  "corroborating members enrich a match but their absence does not degrade it"
  (`obsd/internal/binding/report.go:40`). So a `none`/`partial` mark is a real
  required-member gap, not a cosmetic one.

These already turn doc 01's *"every (entity,variable) pair has a visible state"* into
something **callable** over MCP.

### 3.2 What the new surface adds — static vs dynamic

The existing tools cover everything Vigil *has a binding for*. They cannot, by
construction, surface a blind spot for a phenomenon that has **no binding at all** —
categories 2(a) `INIT_CONTAINER_FAILURE`/`PDB_VIOLATION` (zero members) and 2(b)
`CERT_EXPIRY`/`DNS_FAILURE`/`ETCD_SLOW_PATH` (no modality). A pair with no binding
never enters the ledger. The new registry fills exactly that hole, distinguishing two
provenances:

| Provenance | Source | Examples | Charter class |
|---|---|---|---|
| **STATIC** (from the ontology) | known phenomena the KG *names* but for which there is no ingest path or no authored member on any cluster | 2(b) modality-absent; 2(a) zero-member phenomena; 2(c) epistemic floors | MEASURED *about own coverage* (a fact about the ontology vs the ingest catalogue) |
| **DYNAMIC** (from runtime) | a lane disabled or a signal flat/unobtainable on **this** cluster right now | 2(d) env-induced; a lane returning `available:false`; obtainability `out-of-scope` / `indeterminate` | MEASURED *about own coverage* (a deterministic re-projection of the bound graph + facts) |

The dynamic provenance is *already* computed: the obtainability gate
(`obtain.go:212-260`) lands every authored signal in `Obtainable` /
`OutOfScopeUnobtainable` / `Indeterminate` with a reason string, and the silence
ledger's `no-stream-key` / `out-of-scope` classes are its surface. The static
provenance is the new piece — a small authored table of *named-but-uningested*
phenomena (§6).

### 3.3 Per-query relevant-blindspot hints

The manifest is the full list; a *hint* is the subset relevant to the current
incident. PROPOSED mechanism: when the AI walks the incident playbook
(`obsd/internal/mcp/server.go:180-184`), the blindspot tool can be called with the
phenomenon ids or workloads in play and return only the blind spots **adjacent** to
them — e.g. for a degraded front-end whose dependency is a DNS-dependent service,
surface the `DNS_FAILURE` modality-absent hint. This keeps the AI from drowning in
the whole catalogue while guaranteeing the *relevant* "I cannot see X here" reaches
the synthesis step. The hint is advisory and additive; it never suppresses a fact.

---

## 4. Worked example — a hint that changes the answer

**Incident.** The front-end SLO (request-rate / queue-depth) is breached; the
root-cause chain (`get_root_cause_chain`) stitches `front → mid` but the deepest
degraded callee `mid` shows **no degraded dependency** — a silent intermediate. The
AI also reads `get_topology`: `mid` calls an external-name service resolved through
CoreDNS.

**Without the blindspot surface.** The AI sees a degraded `mid` with a healthy-looking
dependency graph and synthesizes: *"mid is the root cause; its dependencies are
healthy, so the fault originates in mid."* This is an **over-claim**: it asserts a
near-MEASURED cause that rests on the *absence* of a signal Vigil never had.

**With the blindspot surface.** The per-query hint (§3.3) returns the 2(b) entry:
`DNS_FAILURE — modality-absent — Vigil ingests no CoreDNS/resolver series on this
cluster (would need CoreDNS/CNI metrics).` The silence ledger confirms no resolver bar
is watched for `mid`. The AI now synthesizes, correctly and within the charter:

> "MEASURED: `mid` is degraded (request-rate over its declared SLO); the root-cause
> chain shows no degraded downstream dependency [cite `get_root_cause_chain`]. **Vigil
> cannot observe DNS resolution on this cluster** [cite blindspot: `DNS_FAILURE`,
> modality-absent] — `mid` resolves an external dependency through CoreDNS, which is
> outside Vigil's sensors. *My hypothesis* (not a Vigil fact): check CoreDNS health
> and `mid`'s resolver latency out of band before concluding the fault is internal to
> `mid`."

The hint flipped a false MEASURED cause into (a) the grounded MEASURED facts, (b) a
stated limit, and (c) a labelled hypothesis with an actionable out-of-band check. The
remediation register matches the server's own instruction that DNS-class fixes are the
AI's to reason from ops knowledge, on Vigil's grounded facts (`server.go:175-184`).

---

## 5. Charter framing — state absence, never fabricate a phenomenon

The blindspot surface is the honesty discipline extended to the MCP boundary, and it
obeys the same three rules as every other surface:

1. **It STATES absence; it never fabricates a phenomenon.** The manifest enumerates
   what Vigil *cannot* observe. It never emits a finding, never invents a bar
   (borrowed normativity: where nothing is declared, the ledger says `unbounded`,
   `silence.go:31-34`), and never claims a phenomenon is *occurring* — only that it is
   *unobservable*. A blind spot is the absence of a sensor, asserted as a fact about
   coverage, not a guess about the cluster.

2. **Classes are preserved.** The whole surface is **MEASURED about own coverage** —
   the same class the silence ledger and coverage report already carry
   (`silence.go:19-21`, `coverage.go:46`). It joins, never fuses: a blindspot hint sits
   *adjacent* to the MEASURED chain and the AUTHORED relations in the AI's context,
   each labelled, exactly as doc 01 §4 permits at the one surfacing layer. The AI's
   *response* to a hint (a hypothesis, a recommended check) is the AI's own
   **ADVISORY** class — separate, labelled, never written back
   (`obsd/internal/mcp/advisory.go:9-31`), and still charter-checked: a draft that
   asserts an un-authored cause as fact is REFUSED by the register guard
   (`api/charter.go:32-85`, applied in `advisory.go:40-46`).

3. **No model output re-enters the graph.** The blindspot registry is sourced from the
   ontology + the runtime facts, never from the AI. The AI consumes the manifest; it
   never authors it. This preserves the charter's hardest line — "there is no learned
   edge, weight, or threshold anywhere" (doc 01:38).

The register linter is, honestly, a **backstop, not a proof** — the primary guarantee
is structural: the surfacers generate no free causal prose, only classed facts and
verbatim authored notes (`api/charter.go:26-31`). The blindspot surface adds no
generative affordance; it only *states more of what Vigil already knows it cannot
see.*

---

## 6. Implementation notes (PROPOSED)

A single new read-only MCP tool, `get_blindspots`, returning the blindspot registry.
It follows the exact pattern of the existing read tools (a reader func on
`mcp.Sources`, an entry in `toolDefs()`, a dispatch case — `server.go:23-47, 188-262`),
so no-write-back stays **structural** (no setter, no mutable handle in scope).

**Tool contract.**
- Name: `get_blindspots`. Class: `MEASURED (about own coverage)`. Empty input schema.
- Returns `{ available, generatedAt, graphVersion, graphRelease, static[], dynamic[],
  note }`, mirroring the honest-empty-state shape the other views use
  (`Available bool`, `coverage.go:78-85`; `silence.go:54`). `available:false` when
  binding has not compiled — stated, never a misleading empty list.
- Each entry: `{ phenomenonId, label, category, provenance, reason, unblockedBy }`
  where `category ∈ {unwired-reachable, modality-absent, epistemic-floor,
  env-induced}` (the §2 taxonomy) and `provenance ∈ {static, dynamic}` (§3.2).
- A lane being off returns `{available:false, note:…}` rather than an error — the same
  honest-lane-status convention the relay tools use (the reader funcs return nil when
  their lane is off; `server.go:22-23`).

**Sourcing.**
- **STATIC** (`static[]`): a new authored table under
  `ontology/graph/overlays/experimental/` listing the named-but-uningested phenomena
  with their category, reason, and `unblockedBy` ingest path. Authored only via
  governance (doc 12) — it is AUTHORED content about coverage, kept terse and
  verbatim, with author + version provenance like every other note (doc 01:25). The
  v0.8.0 seed is §2(b) (modality-absent), the zero-member entries of §2(a)
  (`INIT_CONTAINER_FAILURE`, `PDB_VIOLATION`), and the §2(c) epistemic floors as fixed
  text. The epistemic floors are *constants* of the architecture, not per-cluster, so
  they live here unconditionally.
- **DYNAMIC** (`dynamic[]`): a pure re-projection of `binding.Result` +
  `AvailabilityReport`, identical in spirit to how the silence ledger is built
  (`silence.go:19-27`). Each `OutOfScopeUnobtainable` / `Indeterminate` signal and each
  `no-stream-key` / `out-of-scope` silence row becomes a dynamic blindspot entry,
  carrying the obtainability reason string verbatim (`obtain.go:48-53, 215-260`). Same
  `Result` + same facts ⇒ byte-identical `dynamic[]` (the determinism guarantee).
- **Per-query hints** (§3.3): a parameterised variant or a filter argument
  (PROPOSED) `{ phenomenonIds?: string[], workloads?: string[] }` that returns the
  registry subset adjacent to the incident; absent args ⇒ the full manifest. The
  filter is purely a projection over the same registry — it withholds nothing the full
  call would not also return.

**Determinism + test posture.** The dynamic half is a deterministic re-projection, so
it gets a golden-fixture test reconciling its counts against the silence ledger and
the coverage report (the silence ledger already does this cross-check,
`silence.go:24-27`). The static half is validated by `tools/graphlint` like any other
overlay. The tool result is charter-scanned by `api.CharterViolations` like every
other payload (`api/charter.go:71-85`).

---

## Caveats (honesty about this doc)

- Phenomenon counts (**11 WIRED**, ~30 unwired) reflect the **v0.8.0** snapshot
  (2026-06-17); they are a snapshot, not a ceiling. Future releases wire more (e.g.
  task #82 level-based leak, task #138 feasible implementation-debt phenomena), so the
  static registry must be re-derived per release, not frozen.
- The **environment constraints** in §2(d) (PSI kernel ≥ 4.20, `kubelet_volume_stats=0`
  on kind, `container_oom_events_total=0`) are **hardness facts for a cluster type, not
  design gaps**. kind is deliberately constrained for dev (doc 14 §3.1 honest caveat);
  production measurement differs. The dynamic registry will report *different* env
  blind spots on a CSI/control-plane-scraped cluster — by design.
- The **harm × reachability classification** and the per-phenomenon reachability
  blockers (§2(a)/(b)) are taken from the coverage-harm audit (branch v4, 2026-06-17),
  treated as authoritative for ranking but unverified for the entries the audit itself
  flags as feasibility-unverified (`CNI_FAILURE`, `PDB_VIOLATION`).
- The **register linter is a backstop, not a proof** (`api/charter.go:26-31`,
  `advisory.go:21-23`). The blindspot surface relies on the *structural* guarantee
  (no generated prose) as the primary defence; the denylist only catches obvious
  violations if dirty data reaches the boundary.
- `get_blindspots` is **PROPOSED**. The substrate it composes — silence ledger,
  coverage report, obtainability gate, read-only MCP server — is built and live; the
  registry tool and its static overlay are not yet implemented.
