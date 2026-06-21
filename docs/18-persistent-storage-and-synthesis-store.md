# 18 — Persistent Storage & Synthesis Store

**Role in suite:** A two-part document. **Part A** is an honest inventory of what
Vigil *currently* persists to disk versus what lives only in memory and is lost on
restart — grounded in the code, with each store's provenance class stated. **Part B**
is a **PROPOSED** expansion: a synthesis-ready, append-only **classed-fact ledger**
that gives an MCP agent (or a human) a cross-*time* substrate to mine, without
violating the charter (doc 01). Everything in Part B is labelled PROPOSED and is *not
built*; Part A is the verified current state.

> Read alongside [`docs/14-implementation-clarifications.md`](14-implementation-clarifications.md)
> §2 (store persistence verdict), [`docs/01-epistemic-separation-charter.md`](01-epistemic-separation-charter.md)
> (the provenance constitution), and [`docs/10-surfacing-and-operator-experience.md`](10-surfacing-and-operator-experience.md)
> (the surfaces that read these stores).

---

## Part A — Current Persistent Storage (verified)

### 1. Storage inventory

Vigil keeps two distinct durable substrates and a large amount of deliberately
ephemeral in-memory state. The two durable substrates are:

1. **A single SQLite database** holding three surfacing tables (findings,
   unexplained, incidents) — pure-Go `modernc.org/sqlite`, CGO-free
   (`obsd/internal/store/findings.go:9`), opened once with WAL and a one-connection
   pool (`findings.go:93-108`).
2. **The warm-tier time-series store (`qss`)** — append-only 2 h segment files on
   disk (doc 14 §2.3), the replay/forecast substrate.

Everything else is rebuilt every evaluation tick.

| Store | Package / table | What it holds | Provenance CLASS | Retention | Durable vs ephemeral |
|---|---|---|---|---|---|
| **Findings table** | `internal/store` · `findings` | One row per `(entity_cei, phenomenon, graph_version)`: phenomenon match, quality, completeness, required-member counts, `members_json` / `unobservable_json` snapshot at match instant, `first_seen`→`last_seen` span | **MEASURED** (born MEASURED; a match is a deterministic consequence of facts) | Until deleted (no TTL in code); `--db` file persists across restarts | **Durable** (only if `--db` set; else `:memory:`) |
| **Unexplained table** | `internal/store` · `unexplained` | One row per `(scope, signature, graph_version)` loud-but-unmatched card; status (live/resolved/superseded), `superseded_by`, occurrences, `first_seen`→`last_seen` | **MEASURED** ("not yet explained by any curated pattern" — no causal vocabulary) | Until deleted (no TTL); closed spans kept for the timeline | **Durable** (with `--db`) |
| **Incidents table** | `internal/store` · `incidents` | Cross-run phenomenon memory (v3 T-B): one row per `incident_key`; phenomenon, role CEI, `role_unresolved`, window bucket, `recurrence_count`, `lifespan_seconds`, graph_version as an attribute | **MEASURED** (recurrence is MEASURED arithmetic; deterministic grouping, never learned) | Until deleted (no TTL) | **Durable** (with `--db`) |
| **Warm time-series** | `internal/qss` | Append-only 2 h segment files (fixed-width `ts 8B + value 8B`); the replay substrate + forecast context | **MEASURED** (raw readings) | **7 days** (segment delete by age, doc 14 §2.3) | **Durable** (disk) |
| **Topology log** | replay substrate (doc 14 §1.3) | Appended edge assertions (asserted/confirmed/retracted + timestamps); the topology-snapshot component of replay bundles | **MEASURED** | Follows warm store (**7 days**) | **Durable** (disk) |
| **Bound graph snapshot** | `internal/graph` (doc 14 A8) | In-memory adjacency + a *periodic snapshot file*; reload-then-reconcile on restart. **No graph database.** | **AUTHORED** (the ontology) ⋈ **MEASURED** (live bindings) | Periodic snapshot | **In-memory + snapshot** |

**What is lost on restart when `--db` is unset:** all findings, all unexplained
cards, and all incident memory. The flag is `--db` (`obsd/cmd/obsd/main.go:91`,
`empty = in-memory`); when empty the database is `:memory:` (`findings.go:95-97`) and
every surfacing row evaporates with the process. The warm time-series and topology
log are independent of this flag (they are the replay/forecast substrate, doc 14 §2.1).

#### 1.1 Findings table — schema & provenance

Schema at `obsd/internal/store/findings.go:67-88`; primary key
`(entity_cei, phenomenon, graph_version)`. `UpsertFindings`
(`findings.go:119-159`) sets `first_seen` on a new key and advances `last_seen` on a
recurring one (`ON CONFLICT ... DO UPDATE`), preserving the span. The row is born
**MEASURED**: the `quality` column carries the `Quality` enum and `members_json`
encodes the member set + thresholds *at the match instant* — a snapshot, not live
state (`findings.go:145-151`).

**Staleness is never persisted.** `Stale` and `LastSeenAgoSeconds` are *derived at
serve time* by `MarkFreshness` (`findings.go:55-65`, "Pure; serve-time only — the
stored row is never mutated"). The horizon is `3 × evaluation tick`
(`obsd/cmd/obsd/main.go:651`). The durable store can hold a finding whose last match
was many ticks ago; a surface marks it stale rather than presenting it as firing now.

#### 1.2 Unexplained table — schema & provenance

Schema at `obsd/internal/store/unexplained.go:18-35`; key
`(scope, signature, graph_version)` where the signature is the sorted uncovered-metric
set (`unexplained.go:128-146`). `UpsertUnexplained` (`unexplained.go:56-93`) keeps a
resolved/superseded card's row (the timeline wants the closed span). This is the
*stated blind spot* (doc 08): MEASURED only, no causal vocabulary, mandatory
"not-yet-explained" mark (doc 01).

#### 1.3 Incidents table — schema & provenance

Schema at `obsd/internal/store/incidents.go:17-31`. The key is
`sha256(phenomenon | role-CEI | bucket)` (`internal/incident/incident.go:40-49`),
deterministic and **immutable across graph-version bumps** — graph version is
*deliberately absent* from the key (`incident.go:37`) so a phenomenon-definition bump
does not split a recurring incident. `UpsertIncident`
(`incidents.go:52-107`) increments `recurrence_count` **only across a resolve gap**
(`incidents.go:81-82`, `if at.Sub(last) > resolveHorizon`), never per tick, applying
the *same* deterministic logic as `incident.Accumulator` (cross-checked in
`incidents_test.go`) so the live durable memory and the replay-gate fold can never
diverge. `resolveHorizon` and `bucketSize` are declared parameters, not magic
constants.

### 2. What is NOT persisted (in-memory ephemeral)

Per the doc-14 §2.1 verdict, in-memory rings are "the permanent design for the hot
path," not a shortcut. The following are rebuilt every tick and lost on restart:

| Ephemeral state | Where | Why not persisted |
|---|---|---|
| **Hot rings** (60 min/stream) | `internal/qss` | The **only** thing the deterministic path reads; fingerprint eval needs minutes, never disk (doc 14 §2.1, lines 66-68). Repopulates from live scrape on restart. |
| **Role bindings** (membership in Roles) | rebuilt in the main loop from the live identity store | Identity is reconstructed from live cluster state each tick; no store write for bindings. |
| **Selection state** (Tier A/B membership) | `internal/selection` | Recomputed per tick; Tier-B "confidence" is a MEASURED *count*, not a learned score (deterministic params). |
| **Matcher state** (co-occurrence windows, offsets) | `internal/detect` | Parameter-driven windows, no state carried between ticks. |
| **Live findings & cascades** | materialized per tick by `internal/detect` | A cascade is a MEASURED span set matched against an AUTHORED edge (join, never fuse, doc 01 lines 35-36); recomputed, then *surfaced into* the findings table. |
| **Binding / coverage state** (bound/suspect/unresolved/out-of-scope) | `coverage.Store`, rebuilt per tick (`main.go:989`) | A MEASURED honesty statement about the system's own visibility (doc 01 line 27); recomputed, surfaced as `CoverageView`. |
| **Silence ledger** (the deterministic ABSENCE map) | rebuilt per tick | Every `(entity,variable)` pair is WATCHED or SILENT-with-reason; MEASURED, recomputed, not persisted. |
| **Context windows** (operator annotations) | `internal/api` · `ContextWindowStore` | **In-memory map (v1)** (`context.go:58-65`); an ANNOTATION carrying *no* provenance class of its own (`context.go:23-25`); `SpliceEligible` derived from kind (`context.go:95-97`). Persists only if a backing store is added in Phase 3 — *stated*, not built. |

### 3. The MCP server & synthesis layer (read-only relay)

The MCP adapter (`internal/mcp`) is **READ-ONLY** (`doc.go:1`, `:24-28`). Its
`Sources` struct is intentionally a set of *reader funcs* with no setter, writer, or
mutable handle in scope, so no-write-back is **structural**
(`server.go:18-47`). It exposes **~27 tools** (`server.go:toolDefs()`): the already-classed
view readers — `get_coverage`, `get_silence_ledger`, `get_warnings`, `get_incidents`,
`get_events`, `get_insights`, `get_root_cause_chain`, `get_cross_service`,
`get_topology`, `get_unexplained`, `get_departures`, `get_authored_relations`,
`get_blindspots`, `get_log_templates`, `get_dependency`, `get_audit_changes`,
`get_trace_graph`, `get_timeline`, `get_onsets`, `get_causal_hypotheses`, `get_findings`,
`get_config`, `get_candidates`, `get_provisional_coverage`, `get_governance` — plus
`validate_claim` (the honest-labeler referee, v3 T-D — *never* blocks) and
`emit_advisory` (returns a labelled **ADVISORY** 4th class to the caller and **writes
nothing anywhere**, `doc.go:26-28`; gated until its backtest passes).

The server is **NON-GATING** (`doc.go:29-31`): it reads immutable per-tick snapshots;
the deterministic eval/detect path never calls into it; with the server absent or
disabled, detection is byte-identical.

**Current limitation — only "now".** Every tool reads the *current* snapshot.
`ActiveFindings` / `ActiveIncidents` return newest-first with a default limit of 100
and **no temporal filter** (`findings.go:162-191`, `incidents.go:110-140`). There is
no time-travel query, no `as-of` parameter, no "show me the evidence that preceded
this 30 minutes ago." An agent gets the present state of the three tables plus their
recurrence counts — but cannot reconstruct *how the present arose*.

---

## Part B — PROPOSED Synthesis Store (not built)

> Everything below is **PROPOSED**. None of it exists in the code today. It is a
> design for a charter-compliant, append-only, cross-time ledger.

### 4. The gap: Vigil persists "now," not a queryable history

The three SQLite tables and the warm time-series are each a *current-state-plus-span*
view:

- Findings carry `first_seen`→`last_seen` but **collapse to one row per
  `(entity, phenomenon, graph)`** — a finding that fired, cleared, and re-fired is the
  *same row* with an advanced `last_seen`. The resolve/refire structure is lost.
- Incidents carry a `recurrence_count` but **not the timeline of episodes** — you know
  it recurred 4 times, not *when* each episode opened or cleared, nor what else was
  degraded alongside it.
- The warm time-series holds raw floats but **no classed phenomenon events** — you can
  replay the numbers but not query "which authored relations activated last week."

Concretely, the following are **missing** today:

- **An episode-level audit trail** — onset/clear timestamps for *each* firing episode
  (not the collapsed span), so "this OOM recurs every ~6 h" is queryable as discrete
  events rather than a single count.
- **A member-state timeline** — when each member signal joined/left a phenomenon and
  which bar it crossed at the instant of joining.
- **A forecast-outcome record** — the PROJECTED band Vigil emitted, kept *beside* the
  MEASURED reality that later did or did not cross it (today the warning evaporates;
  there is no calibration substrate).
- **A binding/coverage delta log** — coverage is recomputed per tick and *never*
  persisted, so "we went blind to disk pressure at 14:02 because the exporter dropped"
  is unrecoverable.
- **An authored-relation activation log** — when a curated cascade edge lit up, so
  blast-radius recurrence is mineable.

**Why this matters for synthesis:** the MCP synthesis relay (doc 17 / the v3.1 work)
asks an AI to *narrate Vigil's already-computed cause* from grounded classed facts. An
agent handed only "now" cannot say "this is the third time currencyservice has leaked
into an OOM this week, and the last two preceded a checkout latency breach by ~9 min" —
because that is a JOIN across *time* of MEASURED episodes and PROJECTED warnings, and
the substrate to join does not exist.

### 5. PROPOSED append-only classed-fact ledger

#### 5.1 Design principles

1. **Append-only.** Rows are never updated or deleted in place (retention prunes whole
   *old* rows; it never mutates a kept one). This is what makes the ledger an audit
   trail rather than a second mutable cache.
2. **Classed at birth, immutable.** Every row carries exactly one provenance class in a
   `class` column, assigned by the producer, never rewritten — identical to the
   existing tables.
3. **Off the deterministic path.** Writes happen in the same surfacing lane as today's
   findings store: a write failure is *surfaced, never fatal* (the existing handler is
   `main.go:650-651`, "findings store: upsert failed (surfacing only)"). Detection stays
   byte-identical whether the ledger is present, degraded, or absent.
4. **Same SQLite DB.** New tables live beside `findings`/`unexplained`/`incidents`
   (migrated in the same `Open`, `findings.go:104`), so there is one file to back up
   and one connection to pin.
5. **Join, never fuse.** Cross-class rows are *never* merged into one row of a stronger
   class. A forecast and its later reality sit in **two rows** (or two labelled columns),
   each classed; the agent joins them at read time.

#### 5.2 Finding-episode log (CLASS = MEASURED)

The discrete episodes the collapsed findings table cannot express. One row **per
onset**; `cleared_at` is filled by an append of a *clearing* row keyed back to the
onset (append-only: the onset row is not mutated).

| Column | Type | Notes |
|---|---|---|
| `episode_id` | TEXT PK | `sha256(entity_cei | phenomenon | onset_at)` — deterministic |
| `entity_cei` | TEXT | the firing entity |
| `phenomenon` | TEXT | authored phenomenon id (verbatim) |
| `graph_version` | TEXT | attribute, not identity |
| `onset_at` | TEXT | first tick this episode crossed |
| `cleared_at` | TEXT NULL | filled when a later clearing row resolves it |
| `quality` | TEXT | quality enum at onset |
| `members_json` | TEXT | member set + bars at onset (snapshot) |
| `class` | TEXT | constant **`MEASURED`** |

#### 5.3 Forecast-outcome log (TWO classes, side-by-side, NEVER fused)

The calibration substrate. Each row pairs a PROJECTED band with the MEASURED reality
that *later* resolved it. The band columns are PROJECTED; the resolution columns are
MEASURED; the `class` of the *row* is split explicitly so neither launders into the
other.

| Column | Type | Class | Notes |
|---|---|---|---|
| `forecast_id` | TEXT PK | — | `sha256(series_key | issued_at)` |
| `series_key` | TEXT | — | the (CEI, variable) the clock ran on |
| `issued_at` | TEXT | — | when the warning was emitted |
| `bar` | REAL | borrowed-normativity | the customer/config bar (NOT learned) |
| `band_earliest` | TEXT | **PROJECTED** | near edge of the crossing band |
| `band_latest` | TEXT NULL | **PROJECTED** | far edge; NULL = open band (beyond horizon) |
| `resolved_at` | TEXT NULL | **MEASURED** | when reality actually crossed (or NULL = never within horizon) |
| `resolution` | TEXT NULL | **MEASURED** | `crossed-in-band` / `crossed-outside-band` / `no-crossing` |

The band **never collapses to a line** (doc 01): if `band_latest` would equal
`band_earliest` the row is rejected, exactly as the departure lane rejects a zero-width
band. The MEASURED resolution is a *separate* column the agent reads adjacently — it is
never written back into the band.

#### 5.4 Incident-lineage log (CLASS = MEASURED)

Today's `incidents` table holds the current recurrence *count*; this log holds the
discrete *episodes* behind that count, keyed to the same deterministic `incident_key`.

| Column | Type | Notes |
|---|---|---|
| `lineage_id` | TEXT PK | `sha256(incident_key | episode_onset)` |
| `incident_key` | TEXT | FK to the existing `incidents` row (same sha256, `incident.go:40-49`) |
| `episode_onset` | TEXT | when this distinct episode opened |
| `episode_clear` | TEXT NULL | when it cleared (append-only fill) |
| `co_degraded_json` | TEXT | other CEIs MEASURED-degraded in the same window (co-occurrence, **not** cause) |
| `class` | TEXT | constant **`MEASURED`** |

#### 5.5 Binding/coverage-delta log (CLASS = MEASURED)

Coverage is recomputed per tick today and discarded. This log appends a row only when a
`(entity, variable)` pair *changes* visibility state, so coverage expansion/contraction
becomes queryable history.

| Column | Type | Notes |
|---|---|---|
| `delta_id` | TEXT PK | `sha256(entity_cei | variable | changed_at)` |
| `entity_cei` | TEXT | |
| `variable` | TEXT | |
| `from_state` | TEXT | `bound` / `suspect` / `unresolved` / `out-of-scope` / `silent-no-stream` |
| `to_state` | TEXT | same enum |
| `reason` | TEXT | the exact silence reason (mirrors the silence-ledger reason) |
| `changed_at` | TEXT | |
| `class` | TEXT | constant **`MEASURED`** (a fact about the system's own visibility, doc 01 line 27) |

#### 5.6 Authored-relation activation log (CLASS = AUTHORED ⋈ MEASURED, two columns)

When a curated cascade/relation edge *manifested* (both ends MEASURED-degraded in
window). The relation itself is AUTHORED (surfaced verbatim with author+version); the
*activation* is the MEASURED co-occurrence that lit it. Stored in two labelled columns,
never one fused "cause" string.

| Column | Type | Class | Notes |
|---|---|---|---|
| `activation_id` | TEXT PK | — | `sha256(relation_id | activated_at)` |
| `relation_id` | TEXT | **AUTHORED** | the curated edge id |
| `relation_note` | TEXT | **AUTHORED** | the verbatim authored "why" + author + version |
| `trigger_cei` | TEXT | **MEASURED** | the upstream degraded end |
| `downstream_cei` | TEXT | **MEASURED** | the downstream degraded end |
| `activated_at` | TEXT | **MEASURED** | when the co-occurrence held |

#### 5.7 Departure-event log (CLASS = PROJECTED)

The band-departure anomaly lane (`internal/departure`) is gate-pending and off-digest
today; a persisted log would let it be mined for recurrence once its gate flips.

| Column | Type | Notes |
|---|---|---|
| `departure_id` | TEXT PK | `sha256(series_key | departed_at)` |
| `series_key` | TEXT | |
| `departed_at` | TEXT | the sample that left its own forecast band |
| `band_json` | TEXT | the band it left (non-collapsing; malformed/zero-width never stored) |
| `margin_fraction` | REAL | the STRUCTURAL margin (band-width fraction, **not learned**) |
| `class` | TEXT | constant **`PROJECTED`** |

### 6. Synthesis enabled — new JOINS the ledger makes possible

Every example below is a **JOIN of labelled classes across time** — never a new fused
or causal claim. The agent narrates the join; it does not author a cause.

1. **Recurring co-occurrence (MEASURED ⋈ MEASURED).** Join `finding_episode` (§5.2)
   with `incident_lineage.co_degraded_json` (§5.4): "currencyservice OOM and
   checkout-latency-breach have co-occurred in 3 of the last 4 episodes." This is a
   *co-occurrence count*, explicitly MEASURED — it is *not* a causal claim, and the
   agent must say so. If an AUTHORED relation also activated in those windows (§5.6),
   the agent may add the authored "why" verbatim *beside* the count, never as a fused
   "because."
2. **Forecast calibration (PROJECTED ║ MEASURED, side-by-side).** Read `forecast_outcome`
   (§5.3): "of the last 20 working-set warnings, the MEASURED crossing fell inside the
   PROJECTED band 18 times; twice it never crossed (no-crossing)." The band stays
   PROJECTED, the outcome stays MEASURED; the agent reports calibration without ever
   restating a projection as a measurement.
3. **Blast-radius recurrence (AUTHORED activation ⋈ MEASURED episodes).** Join
   `authored_relation_activation` (§5.6) over time: "the authored
   `THROTTLING_CASCADE → PROBE_FAILURE_RESTART` edge has activated 5 times this week,
   each time on the same downstream CEI." The recurrence is MEASURED; the relation is
   AUTHORED-verbatim; the agent narrates that the authored relationship was repeatedly
   *manifest*, not that it was *proven*.
4. **Coverage-aware honesty (MEASURED visibility ⋈ MEASURED episodes).** Join the
   coverage-delta log (§5.5) with the episode log: "this phenomenon was SILENT
   (no-stream-key) from 14:02–14:31, so the absence of episodes in that span is a blind
   spot, not a quiet period." This is exactly the honest-partial-coverage discipline,
   now answerable over time.
5. **Lead-time mining (PROJECTED issuance ⋈ MEASURED resolution).** Join `forecast_outcome.issued_at`
   with `resolved_at`: "warnings on this series have led the MEASURED crossing by a
   median of ~9 min." A measured lead time over a band — never a promise.

In every case the two operands keep their labels; the result is a *join*, presented
adjacently, at the one place the charter permits (the surfacing/synthesis boundary).

### 7. Charter guardrails — what must NEVER be stored

The ledger is only legitimate if it cannot become a side-channel for fusion or learning.
The rules, drawn from doc 01:

- **No model output as a row.** A clockd forecast may be stored *only* as a PROJECTED
  band with its non-collapsing bounds (§5.3, §5.7). No generated *text* — no synthesized
  "reason," no AI narration — is ever persisted. Reasons are only human-authored graph
  notes, surfaced verbatim (§5.6 `relation_note`).
- **No learned edge, weight, or threshold.** Every `bar` / `margin_fraction` column is
  borrowed normativity (customer config) or a STRUCTURAL constant — never a value
  derived from observed behaviour. There is no row anywhere whose number a model chose.
- **No row of a stronger class than its inputs.** A row's `class` is the *weakest* of
  its inputs. The forecast-outcome row therefore does **not** carry a single row-level
  class — its PROJECTED and MEASURED columns are labelled independently, and no
  consumer may read the pair as one MEASURED fact.
- **No fusion column.** There is no `cause` column, no `because` field, no boolean
  "X caused Y." Co-occurrence is stored as a co-occurrence (§5.4 `co_degraded_json`);
  authored cause is stored as an AUTHORED note (§5.6); they are joined at read time,
  never at write time.

**How immutability + append-only is enforced:**

- Writers use `INSERT` only; there is no `UPDATE` path for a kept row's facts. A
  "clear"/"resolve" is a *new appended row* keyed back to the onset (§5.2, §5.4), so the
  onset record is never rewritten — mirroring how `incidents` already keeps a resolved
  card rather than deleting it.
- The producer stamps `class` at insert; no API exists to change it (the same structural
  guarantee the MCP `Sources` struct gives by having no setter, `server.go:18-47`).
- A charter-battery test (doc 11 §M6) asserts: every ledger row carries exactly one
  class per column; no forecast-outcome row reads as a single MEASURED fact; no `cause`
  column exists; detection digests are byte-identical with the ledger on, off, or
  failing.

### 8. Implementation notes

- **Engine:** the existing SQLite DB (`modernc.org/sqlite`, CGO-free), migrated in the
  same `Open` (`findings.go:104`), one pinned connection, WAL. No new dependency.
- **Append-only + deterministic:** every `*_id` is a `sha256` of deterministic fields
  (the pattern already used by `incident.Key`, `incident.go:40-49`), and every write
  takes an *injected* instant — no `time.Now` in logic — so the ledger is replay-stable.
- **Off-path writes:** the ledger writers run in the surfacing lane next to
  `UpsertFindings`, with the same non-fatal error handling (`main.go:650-651`). A failed
  ledger write logs and continues; detection never waits on it (doc 01 non-gating).
- **Retention:** prune whole rows older than a parameter (initial proposal: **30 days**
  for episode/incident-lineage/activation logs; the forecast-outcome log kept longer —
  calibration wants history — say **90 days**, both declared in the params file, never
  magic constants). Pruning deletes *old whole rows*; it never edits a kept row, so
  append-only immutability holds within the retention window.
- **Query surface:** read-only `As-Of` and `Between(t0, t1)` query funcs added to
  `internal/store`, exposed to the MCP relay as **new read-only tools** (e.g.
  `get_episode_history`, `get_forecast_calibration`) — added to the `Sources` reader-func
  struct exactly like the existing read-only tools, so no-write-back stays structural.

---

## 9. Caveats & honest limits

- **Part B is PROPOSED, not built.** No table in §5 exists in the code today. The
  current verified state is Part A only.
- **An alerts/notification lane now exists** (doc 30) — owning package
  `obsd/internal/notify` (`store.go` is the durable alert store in `alerts.db`, with
  `dispatch.go`/`gmail.go` transport and `cmd/obsd/notify.go` the where/when mapping),
  gated by `--alerts-enabled` (default off) and enforced off-digest by an import-firewall
  test. It is a separate off-digest, non-gating lane, **not** part of this proposed
  synthesis ledger. The earlier WhatsApp/OpenWA sketch was scrapped in favour of Gmail
  SMTP. A richer alert-*history* synthesis store (ACK/snooze, recurrence mining) remains a
  future track.
- **The charter is the hard constraint, not a guideline.** Any storage expansion that
  persisted model text as a reason, a learned threshold, or a fused-class row would fail
  the doc-11 charter battery. The ledger is designed *around* that audit; it is not
  exempt from it.
- **Temporal depth must not break non-gating.** Adding `As-Of`/`Between` queries to the
  MCP relay is safe only because they are read-only and off-path; detection must remain
  byte-identical with the ledger present, degraded, or absent — the same property the
  existing three tables already hold.
- **Kind measurement gaps propagate into the ledger.** What cannot be measured cannot be
  logged: PVC fill is unobservable on kind (local-path emits no `kubelet_volume_stats`),
  `container_oom_events_total` is 0 (OOM is seen via the events lane + pod
  `lastState.terminated`), and PSI needs kernel ≥ 4.20. The coverage-delta log (§5.5) is
  the place these gaps would be *recorded as gaps* — it does not close them.
- **`incident_key` keys on `(phenomenon, role_cei, bucket)`, not an anomaly
  fingerprint.** The incident-lineage log (§5.4) inherits that grouping and its tunable
  `resolveHorizon`; it is deterministic but parameter-driven, not measured heuristically.
