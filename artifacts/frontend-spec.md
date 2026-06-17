# Vigil — Operator Frontend Specification (blueprint, not a build)

> Input to the product owner's own design/plan. Maps **every** backend functionality to
> the operator UI it deserves: pages, KPIs, warnings, graphs, the cluster graph view,
> the unified anomaly surface, and a new **MCP-config page**. Grounded in the live
> system (17 `/api` endpoints + 7 MCP tools) and the real Go view structs + the existing
> brand tokens. Authored 2026-06-16 from a 5-agent inventory workflow.

The non-negotiable rule for the whole UI: **every datum wears exactly one provenance
chip — MEASURED / PROJECTED / AUTHORED — and classes are shown adjacent, never fused.**
The backend literally sets `Class = "MEASURED ⋈ AUTHORED (joined, never fused)"`; the UI
honours that as two side-by-side labelled values, never one blended number.

---

## 0. What exists right now (the reality the UI draws on)

**Backend:** one Go binary (`obsd`) serves **17 `/api` endpoints** + a **7-tool MCP**
server, both on `:9095`. Provenance and gate posture per surface:

| Endpoint | Class | Live posture | Has a page today? |
|---|---|---|---|
| `/api/coverage` | MEASURED (own coverage) | rich | ✅ CoverageReport |
| `/api/findings` | MEASURED | rich | ❌ (only the insights join shown) |
| `/api/insights` | MEASURED ⋈ AUTHORED | rich | ✅ InsightFeed |
| `/api/topology` | MEASURED marks | rich | ✅ TopologyView |
| `/api/cross-service` | MEASURED⋈AUTHORED + PROJECTED (gate-passed) | active | ✅ CrossService (under-uses it) |
| `/api/root-cause-chain` | MEASURED⋈AUTHORED + PROJECTED cap-D (gate-pending) | live | ❌ **headline capability, invisible** |
| `/api/departures` | PROJECTED (gate-pending) | computed, withheld | ❌ |
| `/api/warnings` | PROJECTED | gate-passed, visible | ✅ EarlyWarnings |
| `/api/events` | MEASURED | live | ❌ |
| `/api/incidents` | MEASURED | live | ❌ |
| `/api/unexplained` | MEASURED (stated blind spot) | live | ✅ UnexplainedSurface |
| `/api/silence-ledger` | MEASURED | live | ❌ (only aggregate counts in Coverage) |
| `/api/timeline` | MEASURED + PROJECTED | live | ⚠️ Timeline (drops the projected lane) |
| `/api/context-windows` | un-classed (operator annotation) | live, **only write surface** | ✅ ContextWindows |
| `/api/validate-claim` | ADVISORY referee | live (POST) | ❌ |
| `/api/config` | un-classed (system config) | live | ✅ Config (forecast gate only) |
| `/api/chat` | grounded, cites | live | ✅ Chat (citations unlabelled) |

**The 4 gate constants** decide which PROJECTED lanes are operator-visible:
`phaseECrossServiceGatePassed=TRUE` (cross-service *soon* surfaces),
`phaseDProjectedTransitiveGatePassed=FALSE` (root-cause projected withheld),
`phaseCDepartureGatePassed=FALSE` (departures withheld),
`mcpAdvisoryGatePassed=FALSE` (emit_advisory withheld). A withheld lane still
**computes every tick** but the view returns `active:false` + a verbatim `*PendingNote*`
— the UI must render that note, never an empty panel.

**The existing visual language (reuse, don't reinvent)** — `brand/tokens.css` already
ships a provenance palette keyed on `data-prov`:
- **MEASURED** = teal `#2dd4bf` — solid lines/borders/fills (facts).
- **PROJECTED** = violet `#a78bfa` — always a **shaded cone**, never a solid line; band-fill token `--prov-projected-band: rgba(167,139,250,.18)` is already reserved.
- **AUTHORED** = gold `#d8b765` — always a **verbatim quote with `author@version`**, never restyled into UI prose.

**JSON gotcha:** all views are camelCase **except `flow.Chain`** (embedded by
cross-service + root-cause-chain) which is **snake_case** (`most_upstream_degraded_node`,
`node_class`, `edge_class`, `why_class`, `conn_depth`, `coverage_gaps`, `root_band`). A
chain-consuming component must handle both conventions in one payload.

---

## 1. Direct answers to the owner's questions

**Can I connect Vigil to an MCP and have an AI synthesize across several tools?**
Yes. `obsd --mcp-enabled` serves JSON-RPC 2.0 over HTTP POST at `http://<host>:9095/mcp`
(protocol `2024-11-05`). Any MCP client (Claude Desktop via the `mcp-remote` bridge,
Claude Code via `.mcp.json`, or a raw client) connects and calls the **7 read-only
tools**. The AI *can* synthesize: read coverage, ask the silence ledger what's not
watched and why, pull the forecast warnings (with their bands), the OOM events, the
recurring incidents, and run prose through the referee. See the MCP-config page (§7) for
the exact JSON.

**Can it get the root cause through MCP?** **No — by deliberate design.** There is no
`get_root_cause` / `get_cross_service` / `get_departures` / `get_topology` MCP tool. The
transitive root-cause chain (cap B) and cross-service cascade live **only on `/api`**, so
an MCP client cannot be handed the raw edges to *author* causation. The cost: an MCP-only
AI sees the leak forecast, the OOM event, and the recurrence as separate classed facts
but has no tool that stitches them. **Recommended fix (max-potential):** add a read-only
**`get_root_cause_chain`** relay tool that surfaces the already-built, already-authored
chain verbatim (same pattern as `get_events`: MEASURED edge + AUTHORED why, no new claim).
The AI then *relays* the chain, never derives it.

**What other questions can the AI answer (through MCP today)?**
- *"What can/can't we watch, and why?"* → `get_silence_ledger` (the marquee tool — every
  (entity,variable) pair accounted as watched or silent-with-reason; `totalPairs ==
  watched + silent` by construction).
- *"What's our observability coverage?"* → `get_coverage`.
- *"What's forecast to cross a bar soon?"* → `get_warnings` (PROJECTED bands, register
  "projected to cross", never "will cross").
- *"Has this happened before / what keeps recurring?"* → `get_incidents` (recurrence count).
- *"What OOM/crash events fired?"* → `get_events` (standalone vs corroborated).
- *"Is this sentence I (the AI) am about to say charter-clean?"* → `validate_claim`
  (flags generated causation / class-fusion / future-certainty; rescues authored
  relations; **never blocks**).
- *"Draft an advisory"* → `emit_advisory` (refuses causal/future-certainty drafts;
  withholds even clean ones until its gate flips).

It **cannot** (today) get the root-cause chain, the cross-service cascade, the
band-departure anomalies, or the topology graph — those are web-only.

**Does the system account for anomalies, and how are they identified + notified?** Yes —
**three distinct anomaly notions**, each its own class and surface (never fused):
1. **Capacity-crossing** (MEASURED, visible): a sample crosses a **config-sourced** bar
   (the customer's `vigil.io/slo.*` or a declared limit). Identified deterministically
   every tick → `/api/findings` + `/api/insights`.
2. **Band-departure** (PROJECTED, **gate-pending/invisible**): a MEASURED sample left the
   PROJECTED band its *own* recent forecast drew. The band **is** the bar (structural FP
   defense, not a learned threshold). Computed off-digest → `/api/departures`, withheld
   until task #129's live capture.
3. **Unexplained channel** (MEASURED, the **stated blind spot**, doc 08): loud-but-
   unmatched signal no authored phenomenon claims. Lifecycle new → aging → superseded-by-
   match → resolved; recurring patterns become **curation candidates** (never written to
   the graph). → `/api/unexplained`.
**Notification today is pull-only and there is NO auto-remediation, NO paging** (by
charter). The closest thing to an inbox is the On-Call surface (warnings + insights only).
*Gap → §6: build a unified Anomaly Inbox + ack + on-call routing.*

**Is there a graphical cluster view, and does it help?** Yes — `/api/topology` returns a
real **node-link graph**: `nodes[]{ceiKey,kind,namespace,name,selected,matched,degraded,
loud,warned,phenomena[]}` + `edges[]{type,from,to,status:valid|suspect|retracted}` +
a summary + a `truncated` count (stated, never hidden). It helps a lot: it's the one
place an operator sees **the whole watched cluster with current state overlaid** — which
entities are matched ("is"), degraded, loud (unexplained), or warned ("might", a
deliberately *separate* violet visual family so present-fact and forecast can never be
confused), and which dependency edges are trustworthy (valid) vs degraded (suspect). It's
the natural canvas to highlight a cascade/root-cause path end-to-end. See §4.

---

## 2. Information architecture

Replace the flat 11-tab strip with **intent groups** + a persistent global banner
(cluster id · graph release/version · per-lane gate posture — always on screen):

- **OVERVIEW** — the command center (§3.1).
- **NOW** (MEASURED present): Insights/Findings · Topology graph · Cross-service · Events.
- **SOON** (PROJECTED future): Early-warnings · Anomaly inbox (incl. band-departure) · Root-cause & cascades.
- **MEMORY**: Incidents (recurrence) · Timeline.
- **TRUTH** (the honesty surfaces): Coverage · Silence ledger · Referee · Config · **MCP / Integrations**.
- **MOBILE/ON-CALL**: the triage frame (parity subset).
- **CHAT**: the grounded assistant.

Operator journey: Overview tile → drill to the NOW/SOON surface → follow a cascade into
the Topology graph or the Root-cause chain → check Coverage/Silence to know what's *not*
seen → (optionally) paste a claim into the Referee or ask Chat.

---

## 3. Page-by-page spec

Each KPI/graph below is bound to a **real field**. No invented metrics.

### 3.1 Overview / Command center — `coverage + insights + warnings + silence-ledger + incidents + unexplained + cross-service + root-cause-chain`
**8 grounded KPI tiles:**
1. **Findings by class** — donut from `insights.summary{full,degraded,cascades}`; degraded/total is an *honesty* ratio (amber if >0), `cascades>0` is the headline alarm. MEASURED.
2. **Open forecast warnings** — stat + "soonest crossing" countdown from `warnings.warnings[0].timeToCrossSeconds`; when `enabled:false` render `gateNote` (**not** "0"). PROJECTED.
3. **Observability %** — gauge from `coverage.summary.resolvability` + a 3-segment full/partial/none bar (NONE first); `defaultBars>0` amber (borrowed-normativity tell), `qaFailed>0` red. MEASURED.
4. **Silence-accounting completeness** — progress bar proving `totalPairs == watched + silent` (100% by construction) + watched/silent split + `byReason` stacked bar. **The signature KPI** (deterministic absence). MEASURED.
5. **Cascades active now** — alarm tile from `insights.cascades` + `cross-service.active` + `root-cause-chain.active`; the loudest tile when true.
6. **Recurring incidents** — stat from `incidents.summary.recurring` (recurrenceCount>1); amber if >0.
7. **Unexplained backlog** — count of `unexplained.openCards` new|aging; **always** shows the verbatim `BlindSpotNotice` (never "all clear").
8. **Clock health** — pill from `warnings.clock{ready,degradedSince}` + `config.forecast`; labelled **advisory** (never gates detection).

**Actions:** click any tile → its surface, scoped to the offending entities.

### 3.2 Insights / Findings (NOW) — `/api/insights` (+ a raw `/api/findings` toggle)
**Sees:** summary chips (matched/full/degraded/1-hop/2-hop/cascades/at-risk); a "Cascade
stories" section (`CascadeCard`: trigger ⛓→ downstream, temporal, **authored why
verbatim**); expandable `InsightCard`s — members with MEASURED `state` + `barFlagged` +
the **AUTHORED `note`** (the only "why"), `spanPath` edge-validity, `suspectEdges`,
`blastRadius` (AtRisk rows with authored `why`). **Add** a raw-findings toggle (the
unjoined MEASURED list, with the `stale`/`lastSeenAgoSeconds` distinction rendered — fresh
vs last-seen-long-ago).
**Graphs:** per-finding a small ladder/state strip (well-above > above); a member sparkline
to the bar.
**Actions:** expand, follow blast-radius into Topology, jump to the entity's chain.
**Provenance:** teal MEASURED state, gold AUTHORED note side-by-side per member.

### 3.3 Root-cause & cascades (SOON/NOW) — `/api/root-cause-chain` + `/api/cross-service` — **NEW, the headline, invisible today**
**Sees:** a **left-to-right directed graph** from `flow.Chain`. MEASURED nodes (teal),
each labelling its **own** phenomenon (`upstreamPhenomenon`/`downstreamPhenomenon`); the
`most_upstream_degraded_node` gets a **root ring**. Edges are teal arrows for `edge_class:
"MEASURED observed flow"`, but `edge_traversal == "suspect"` renders **dashed + amber**
(the validity contract). The **`why` sits ON the edge as a gold verbatim quote with
`author@version`** — never a node caption, never "X caused Y". `ChainGap{from,to,reason}`
silent-intermediates render as **dashed grey ghost nodes** ("separated by an unmeasured
node, never bridged" — the CARDINAL no-false-chain honesty). The PROJECTED cap-D lane on
the same surface draws **violet nodes with a band badge that WIDENS each hop** (root
`[12:30..12:50]` → hop1 `[12:20..13:00]` → hop2 `[12:10..13:10]`); while gate-pending,
show `projectedNote` (the withholding reason), not an empty panel. The cross-service
*soon* lane (gate-passed) renders the anticipatory cascade (PROJECTED nodes · MEASURED
edge · AUTHORED why).
**KPIs:** chain count, longest chain depth, # suspect edges, # silent-intermediate gaps.
**Actions:** click a node → its finding; "highlight this path on the topology graph";
toggle now/soon overlay.
**Live-proven shape:** `back→mid→front` (root = deepest degraded callee), and the
cross-service `leakx-callee→leakx-caller` (MEASURED edge, authored "impact travels against
the call arrow").

### 3.4 Early-warnings / Forecast (SOON) — `/api/warnings`
**The band that never collapses.** From `WarningCard`: draw the realized MEASURED series
in solid teal up to `basisAt`, then a **violet shaded cone** (`--prov-projected-band`)
bounded by `earliestAt`(near)/`latestAt`(far) widening rightward, a thin dashed violet
centerline to the `crossAt` point, and a horizontal **bar line at `barValue`** (solid if
`barSource:config`, **dashed + amber if `barFlagged`** = a default bar, lower trust). When
`latestBeyondHorizon:true` the cone's right edge stays **open** with a "may not cross
within horizon" caption. Register fixed to **"projected to cross"** (never "will cross").
`aging` cards show the *last real* projection with an "aging" badge (no flicker, no
fabricated current projection). `precursorPhenomena[]` + `atRisk[]` render as a **gold
sidebar of verbatim authored notes**, never woven into the forecast sentence.
**KPIs:** open warnings, soonest crossing, in-band coverage (~0.80 calibrated), false-
warning count (0).
**The silence list:** every quiet target **with** its guardrail reason (the 10-code
glossary) — "no warning" is a *statement*, not an absence.

### 3.5 Anomaly inbox (SOON/NOW) — `/api/departures` + `/api/unexplained` + capacity-crossings — **NEW unified surface (§6)**
See §6 — aggregates all anomaly notions while keeping one provenance chip per row.

### 3.6 Topology graph (NOW) — `/api/topology` — see §4

### 3.7 Events (NOW) — `/api/events` — **NEW**
**Sees:** discrete k8s events (`reason: "OOMKilled"`, CrashLoopBackOff) as MEASURED
`EventCard`s with `role`/`roleLabel`/`roleUnresolved`, `count`, `firstSeen`/`lastSeen`/
`lastSeenAgoSeconds`, and the **standalone vs corroborated** split. A *corroborated* event
shows its `corroborationWhy` (AUTHORED) verbatim — joined to a gauge phenomenon by shared
role CEI, never fused. A *standalone* event is visible but never upgraded to a match.
**KPIs:** total / corroborated / standalone / unresolved.
**Note:** on kind, OOMs are read from pod `containerStatuses.lastState` (no OOMKilled
Event), so `corroborated:false` standalone is the *correct* state — the blind-spot-closing
property.

### 3.8 Incidents / durable memory (MEMORY) — `/api/incidents` — **NEW**
**Sees:** `IncidentCard`s — `phenomenon`, `role`/`roleLabel`, **`recurrenceCount`**,
`firstSeen`/`lastSeen`, `lifespanSeconds`, sorted by recurrence. The "has this happened
before?" surface. Deterministic grouping by (phenomenon, role) across time — a count,
never a cause.
**KPIs:** total / recurring / unresolved. **Graph:** a recurrence bar per role; a
lifespan timeline.

### 3.9 Timeline (MEMORY) — `/api/timeline` — **fix the regression**
**Sees:** MEASURED match spans + unexplained aging spans **and the live `projected[]`
lane** (currently dropped — it renders a stale "Phase-2 not yet" note; task #83). Map
`projected` as a violet future lane.

### 3.10 Coverage (TRUTH) — `/api/coverage`
**Sees (richest surface):** cluster/graph/release header; SummaryGrid (entities,
resolvability %, the **configBound/configEligible/defaultBars charter floor**,
phenomenaFull/Partial/None, QA verified/suspect/failed); per-phenomenon observability rows
+ `missingReasons[]`; the threshold-rule table (instantiated/configBound/defaultBound/
unbounded/outOfScope); the selection "why these not those" (`noneByReason`).
**KPIs:** resolvability gauge, charter-floor accounting. `defaultBound>0` cells flagged.

### 3.11 Silence ledger (TRUTH) — `/api/silence-ledger` — **NEW, the signature view**
**The deterministic-absence matrix.** Render an **entity × variable heatmap** from
`silent[]` + coverage `rules[]`: rows = entities, columns = rules/metrics, cells with a
5-state legend — **watched** (teal solid) / **unbounded** (grey, "no declared limit —
Tier-B ineligible") / **no-stream-key** (amber-hatch, e.g. the PVC dark-bar: bar resolves,
no series) / **unresolved** (red, "config not readable") / **out-of-scope** (muted). Every
cell hover = the **verbatim `reason`**. Footer = the `totalPairs == watched + silent`
completeness invariant. Give it prime real estate — honest partial coverage is the
product's signature.

### 3.12 Referee (TRUTH) — `/api/validate-claim` — **NEW**
**Sees:** a paste-prose box → POST → `ClaimVerdict`: `flagged`, `Reasons[]` each with its
`class` (generated-causation / class-fusion / future-certainty / fusion / advisory),
`matchedAuthored`, `labelledBestEffort`, `note`. Makes the charter's no-fabricated-
causation discipline **visibly demonstrable** — ADVISORY, **never blocks** (there is no
"blocked" field). Show the rescue case: an authored-relation restatement → `flagged:false,
matchedAuthored:true`.

### 3.13 Config + MCP/Integrations (TRUTH) — `/api/config` + `/mcp` — see §7
Runtime card + forecast-lane gate posture (the honest reason `get_warnings` can be off) +
the new MCP-config page.

### 3.14 Context windows (annotation) — `/api/context-windows`
The only **write** surface: author deploy/config/incident/maintenance windows
(splice-eligible flagged → feed forecast decomposition). Un-classed (operator
annotations), never a finding.

### 3.15 Chat (assistant) — `/api/chat` — **fix citations**
Grounded answers with refusals as first-class; **class-label the citations**
(MEASURED/PROJECTED/AUTHORED) instead of a flat "references:" string (task #83). Must never
upgrade a class or improvise causation.

### 3.16 On-call / Mobile — `/api/warnings` + `/api/insights` (+ the anomaly inbox)
Triage frame: PROJECTED warnings first (band parity), then top MEASURED findings by a
**stated** heuristic (blast-radius/full-quality — *not* an authored criticality score).

---

## 4. Cluster topology graph (detailed)

A node-link force/dagre (or Cytoscape) graph of the watched cluster, current state
overlaid — **distinct glyph families so "is" and "might" can never be confused:**
- `matched` → solid **teal ring** (MEASURED "is").
- `degraded` → teal **hatched**.
- `loud` → **amber dot** (unexplained; links to the Unexplained surface).
- `warned` → **violet pulse-ring** (PROJECTED "might" — a deliberately separate visual family; links to the warning).
- `selected` → thin outline (Tier-A attention).
- Edges: `valid` solid · `suspect` **dashed** (detection degrades across it — never a clean trusted line) · `retracted` ghosted.
- `truncated > 0` → "N entities omitted past the 400 cap" chip (stated, never hidden).

**What it's good for:** the single situational-awareness canvas — *what is watched, what's
firing, what might fire, and which dependency edges are trustworthy* — and the place to
**highlight a cascade/root-cause path end-to-end** (click a matched node → light up its
chain). Today's scaffold renders the marks but doesn't link `warned`→warning,
`loud`→unexplained, or highlight a chain path — all worth wiring.

---

## 5. Provenance visual language (the universal grammar)

| Class | Color | Form | Rule |
|---|---|---|---|
| MEASURED | teal `#2dd4bf` | solid line/border/fill | a fact or deterministic arithmetic on facts |
| PROJECTED | violet `#a78bfa` | **shaded cone**, dashed border, band-fill `rgba(167,139,250,.18)` | a forecast — **never a solid line**; band never collapses |
| AUTHORED | gold `#d8b765` | **verbatim quote** + `author@version` | a curated graph note — never restyled into UI prose |

Cross-class rows (chains, warnings, departures) render each part **side-by-side** with a
visible "⋈ joined, never fused" affordance, mirroring the handler `Class` strings. Add a
small **trust badge** per row: *replay-verified* (MEASURED: capacity-crossing, unexplained)
vs *join-verified, clock-derived* (PROJECTED: band-departure, forecast cascades) — the
doc-15 §5 determinism boundary made visible.

---

## 6. The unified Anomaly Inbox (new first-class surface)

Today the anomaly notions are scattered and On-Call reads only warnings+insights. Build
ONE inbox aggregating all four, **preserving one provenance chip per row** (never an
"anomaly score"):
- **Capacity-crossing** (MEASURED) — teal finding cards; order by ladder state (well-above
  before above) then bar provenance (config above default/flagged).
- **Band-departure** (PROJECTED, gate-pending) — a mini band+outlier chart: the violet
  `[lower,upper]` band with the MEASURED `realized` point **outside** it (above/below),
  `exceedance` as the gap. While gate-pending, render `departurePendingNote` **verbatim**
  as a "gate-pending" state — never a silently-empty page, never a collapsed band. Order
  by `exceedance/bandWidth` (structural, not learned).
- **Unexplained** (MEASURED, blind spot) — an amber "investigate" list (not alarm-red —
  there's no authored meaning yet): `loudStates` + `matchCheck` (no causal vocabulary) +
  the persistent `BlindSpotNotice`; lifecycle new → aging → **superseded-by-match** (link
  to the covering phenomenon) → resolved.
- **Forecast early-warning** (PROJECTED) — the soonest-crossing cards, ordered by `crossAt`.

**New operational features (be explicit they don't exist today):** `ack` (an un-classed
operator annotation — never writes back, never upgrades/suppresses a class) and on-call
**routing/paging** (a delivery concern; `emit_advisory` is the natural no-write-back seam
for an outbound PagerDuty/Slack integration). **No auto-remediation, ever** (charter).
Derive ordering from real fields; **never compute a numeric severity score** (that
fabricates a class).

---

## 7. MCP / Integrations page (the page you asked for)

A TRUTH-group page that turns "connect an AI to Vigil" into a guided, honest flow.

**Panels:**
1. **Connection status** — POST the live `initialize` frame to `/mcp`; show green/red +
   `serverInfo.name` (`vigil-obsd`), `serverInfo.version` (the graph release, e.g.
   `v0.4.0`; empty on an unreleased dev graph — honest), `protocolVersion` (`2024-11-05`).
2. **Copy-pasteable config per client** (one-click copy; editable host field, port fixed
   `9095`, path `/mcp`). The server speaks **plain HTTP JSON-RPC POST** (not stdio, not
   SSE), so stdio-only desktop clients need the `mcp-remote` bridge:

   *Claude Desktop* (`claude_desktop_config.json`):
   ```json
   { "mcpServers": { "vigil": {
       "command": "npx",
       "args": ["-y", "mcp-remote", "http://127.0.0.1:9095/mcp", "--transport", "http-only"]
   } } }
   ```
   *Claude Code* (`.mcp.json` at repo root, or `claude mcp add --transport http vigil http://127.0.0.1:9095/mcp`):
   ```json
   { "mcpServers": { "vigil": { "type": "http", "url": "http://127.0.0.1:9095/mcp" } } }
   ```
   *Generic client* — raw contract: POST only (GET→405), JSON-RPC 2.0, **no auth**, 64 KiB
   body cap, notifications→204, requests→200, and **each `tools/call` result wraps the
   view JSON as a string inside `content[0].text`** (clients must double-parse). A lane
   that is OFF returns `isError:false` with `{"available":false,"note":"<reason>"}` — an
   honest off-state, not an error.
3. **Tool catalog** — all 7 tools, each with its provenance chip
   (get_coverage/get_silence_ledger/get_incidents/get_events = MEASURED; get_warnings =
   PROJECTED; validate_claim + emit_advisory = ADVISORY), its verbatim description, input
   schema (6 no-arg; `validate_claim{claim}`; `emit_advisory{text}`), a runnable example,
   and the real response shape. **Lead with `get_silence_ledger`** ("the provable-negative
   tool", per the server's own `instructions`).
4. **Test connection** — a button firing `initialize` + `tools/list` + one `tools/call`,
   showing the raw response, latency, pass/fail, and **each off-lane note verbatim** so an
   operator sees which flags a dark lane needs (`--incident-memory --db`,
   `--events-enabled`, `--referee-enabled`, the warnings gate).
5. **Capability honesty / missing tools** — state plainly that root-cause-chain,
   cross-service, departures, and topology are **deliberately not MCP tools** (so a client
   can't be handed raw materials to author causation) — a *feature*, surfaced. Pair with
   the recommendation: the planned read-only relay `get_root_cause_chain` +
   `get_cross_service`, and "route any AI-drafted causal sentence through `validate_claim`
   first."
6. **Auth (FUTURE)** — a red "**no auth today** — bind to localhost/cluster-internal only,
   never a public IP" warning (sourced from the `--mcp-enabled` flag help) + a disabled
   bearer-token/mTLS field "pending the auth track."

*Frontend plumbing:* add a `/mcp` proxy entry to `web/vite.config.ts` (mirroring the
existing `/api` → `:9095` proxy); the `obsd` `/mcp` handler sets no CORS headers (a backend
follow-up if the page must call `:9095` cross-origin in prod).

---

## 8. Maximum-potential additions (highest leverage)

1. **`get_root_cause_chain` (+ `get_cross_service`) read-only MCP relay tools** — the
   single change that lets an MCP AI *relay* the authored chain (closes the biggest gap;
   charter-safe because the orientation is authored, not the model's).
2. **MCP auth** (bearer/mTLS) — required before any non-isolated exposure.
3. **Real notification/paging** — an outbound delivery layer on `emit_advisory` (PagerDuty/
   Slack), no-write-back; today everything is pull-only.
4. **The unified Anomaly Inbox + ack + routing** (§6) — the operator workflow the current
   UI lacks entirely.
5. **Build the 7 unwired surfaces** (root-cause-chain, departures, events, incidents,
   silence-ledger, referee, MCP-config) and **fix the 2 regressions** (Timeline projected
   lane; CrossService `coverage_gaps` + projected chain). Class-label Chat citations.
6. **Adopt the mandated libs** (Cytoscape for topology with chain-path highlight; ECharts
   for bands/timeline) and move from 15s polling toward the SSE/Connect stream so "now"
   feels live.

---

## Appendix — build backlog (current UI → target)

- **7 endpoints with no page:** findings (raw), silence-ledger, incidents, validate-claim,
  events, root-cause-chain, departures. **0 of 7 MCP tools** surfaced.
- **2 regressions in existing pages:** Timeline drops the live `projected` lane;
  CrossService drops `coverage_gaps` + the projected anticipatory chain.
- **Under-uses:** Config shows only the forecast gate (no MCP/lane posture); Topology
  doesn't link warned→warning / loud→unexplained / chain-path; Chat citations unlabelled;
  Coverage shows aggregate silence counts but not the per-pair ledger.
- **Structural:** flat 11-tab strip → intent groups + global banner; hand-rolled SVG →
  the mandated Cytoscape/ECharts; 15s poll → SSE.
