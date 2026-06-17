# Vigil — Frontend Interface / Integration Plan (build-ready)

> The exact contract a frontend builds against. Companion to:
> - **`artifacts/vigil-api-types.ts`** — the drop-in TypeScript for all 17 `/api`
>   endpoints + the 7-tool MCP client (79 interfaces, generated from the Go `json:` tags).
> - **`artifacts/frontend-spec.md`** — the page/KPI/graph design blueprint.
>
> Everything below is verified against the live backend (`obsd` on `:9095`, kind-vigil).

---

## 1. Run the backend the frontend builds against

The backend is **one binary** serving `/api/*` **and** `/mcp` on the same `:9095` listener.

```bash
# 0. cluster (kind-vigil) must be up with the boutique + monitoring DaemonSets:
just up            # only if the cluster doesn't exist; otherwise it's already running
just boutique      # Online Boutique + node-exporter + conntrack-agent (one-time)

# 1. the forecasting clock (optional — needed only for /api/warnings + cross-service SOON):
cd clockd && uv run --extra model python -m clockd.server --port 50051 --clock timesfm

# 2. obsd with every lane on (run from repo root):
./bin/obsd --kubeconfig "$HOME/.kube/config" --api --health-addr :9095 --log text \
  --flow-enabled --app-metrics-enabled \
  --app-conditions ontology/graph/overlays/experimental/app-conditions-v1.yaml \
  --events-enabled --incident-memory --db /tmp/vigil-incident.db \
  --mcp-enabled --referee-enabled --departure-enabled --params /tmp/forecast-on.yaml
```
`/tmp/forecast-on.yaml` only needs `forecast: {enabled: true, clockd_target: "127.0.0.1:50051"}`
(the rest defaults). Drop `--params` (and skip clockd) for a MEASURED-only backend — every
other lane works without the clock (**detection never waits on forecasting**).

**Live demo data** (so pages aren't empty): the `appsig` chaos rig fires cap-A L4/L6/L1;
the organic `currencyservice`/`adservice` leaks fire `PHEN_MEMORY_LEAK` + the cross-service
cascade + incidents + events. Re-drive cap A after an `appsig` restart:
`kubectl -n chaos exec deploy/appsig -- wget -qO- 'http://127.0.0.1:8080/ctl?queue=250&frozen=1&rate=120'`.
For a forecast/root-cause demo, redeploy the committed rigs (`corpus/chaos/flow-forecast-p1.yaml`,
`corpus/chaos/chain3-rootcause.yaml`) — but watch node memory (they leak).

**Dev proxy:** add `/mcp` to `web/vite.config.ts` (the `/api` proxy already exists):
```ts
server: { proxy: { "/api": "http://localhost:9095", "/mcp": "http://localhost:9095" } }
```

---

## 2. Endpoint → page → provenance map

| `/api` route | Type (in `vigil-api-types.ts`) | Class | Method | Page it feeds (frontend-spec) |
|---|---|---|---|---|
| `/api/coverage` | `CoverageView` | MEASURED | GET | Overview · Coverage |
| `/api/silence-ledger` | `SilenceLedgerView` | MEASURED | GET | Overview · Silence ledger (heatmap) |
| `/api/findings` | `FindingsResponse` (`FindingRow[]`) | MEASURED | GET | Insights (raw toggle) |
| `/api/insights` | `InsightsView` (`InsightCard`) | MEASURED ⋈ AUTHORED | GET | Insights · Overview |
| `/api/topology` | `TopologyView` | MEASURED | GET | Topology graph |
| `/api/cross-service` | `CrossServiceView` (+ `Chain`) | MEASURED⋈AUTHORED + PROJECTED | GET | Root-cause & cascades |
| `/api/root-cause-chain` | `RootCauseChainView` (+ `Chain`) | MEASURED⋈AUTHORED + PROJECTED (cap-D gate-pending) | GET | Root-cause & cascades |
| `/api/warnings` | `WarningsView` (`WarningCard`) | PROJECTED | GET | Early-warnings (the cone) · On-call |
| `/api/departures` | `DepartureView` | PROJECTED (gate-pending) | GET | Anomaly inbox |
| `/api/events` | `EventsView` | MEASURED | GET | Events |
| `/api/incidents` | `IncidentsView` | MEASURED | GET | Incidents |
| `/api/unexplained` | `UnexplainedView` | MEASURED (blind spot) | GET | Anomaly inbox · Unexplained |
| `/api/timeline` | `TimelineView` | MEASURED + PROJECTED | GET | Timeline |
| `/api/context-windows` | `ContextWindowsView` / `ContextWindow` | un-classed | GET + **POST** | Context windows (write) |
| `/api/validate-claim` | `ClaimVerdict` | ADVISORY | **POST** `{claim}` | Referee |
| `/api/config` | `ConfigView` | un-classed | GET | Config / MCP page |
| `/api/chat` | `ChatResponse` | grounded+cited | **POST** `{question}` | Chat |
| `/mcp` | `McpRequest`/`McpResponse` | transport | **POST** JSON-RPC | MCP-config page + in-app assistant |

---

## 3. The data-fetch layer

**GET (15 of 17 routes) — poll, aligned to the 15 s eval tick:**
```ts
// useApi<T>(path, pollMs = 15_000): UseApiState<T>   — see vigil-api-types.ts §C
const cov = useApi<CoverageView>("/api/coverage");      // cov.data | cov.error | cov.loaded
```
A monotonic-seq guard (already in `web/src/surfaces/useApi.ts`) prevents a slow poll from
overwriting fresher data. **Upgrade path:** replace the poll with an SSE finding-stream or
the Connect-Web client generated from `/proto` (the handlers are stream-ready).

**POST (3 routes + MCP):** `useApi` is GET-only; add a tiny `postJson` helper:
```ts
const verdict = await postJson<ClaimVerdict>("/api/validate-claim", { claim });   // referee
const answer  = await postJson<ChatResponse>("/api/chat", { question });          // chat
const win     = await postJson<ContextWindow>("/api/context-windows", windowBody); // annotate
```
No auth header; `Content-Type: application/json`. A GET on a POST-only route returns **405**.

---

## 4. The MCP client (for the MCP-config page + any in-app assistant)

`POST http://<host>:9095/mcp`, JSON-RPC 2.0. Each `tools/call` result wraps the view JSON as a
**string inside `content[0].text` — you must `JSON.parse` it a second time.**
```ts
async function callTool<T>(name: McpToolName, args?: McpToolArguments): Promise<T | McpLaneOff> {
  const env = await postJson<McpResponse<McpToolResult>>("/mcp", {
    jsonrpc: "2.0", id: 1, method: "tools/call", params: { name, arguments: args },
  });
  if (env.error) throw new Error(env.error.message);          // -32700/-32600/-32601/-32602
  return JSON.parse(env.result!.content[0].text) as T | McpLaneOff;  // 2nd parse
}
// usage — note the McpLaneOff guard (a disabled lane returns {available:false,note}, isError stays false):
const cov = await callTool<CoverageView>("get_coverage");
if ("available" in cov && cov.available === false) showLaneOff(cov.note);
```
Handshake: `initialize` → `McpInitializeResult` (serverInfo `vigil-obsd`, proto `2024-11-05`).
`tools/list` → exactly the 7 `McpToolName`s. A frame with **no `id`** is a notification → **HTTP 204**.
The 7 tools, args, and result types are the `McpToolTextOf` union in `vigil-api-types.ts`.

---

## 5. Critical gotchas (read before wiring)

1. **camelCase everywhere EXCEPT `flow.Chain`** (`/api/cross-service`, `/api/root-cause-chain`)
   which is **snake_case** (`most_upstream_degraded_node`, `node_class`, `edge_class`,
   `why_class`, `coverage_gaps`, `root_band`, `service_ports`, …). The types file quotes
   those keys. One payload mixes both conventions — don't run it through a camelCase
   normalizer.
2. **`/api/findings` ≠ `/api/insights`.** `/api/findings` = raw persisted `FindingRow[]`
   (no members/span on the wire; `Members`/`Unobservable` are `json:"-"`). The rich
   member/evidence/blast-radius trail is on `InsightCard` (`/api/insights`) only. `stale` +
   `lastSeenAgoSeconds` are derived at serve time — render `stale:true` as "last seen Xs ago",
   not firing-now.
3. **The lane pattern = render the note, never an empty panel.** `cross-service`,
   `root-cause-chain`, `departures`, `warnings` carry `{enabled/active, note/gateNote}` (and
   `{projectedActive, projectedNote}`). When `active:false` the data (`chain`/`chains`/
   `departures`/`warnings`) is absent/empty — show the verbatim `note`. A **gate-pending**
   surface (cap-D `projectedActive:false` + pending note; `departures` `active:false` + pending
   note) is the **correct state, not "no data"** — never render it as empty/all-clear.
4. **`barFlagged` / `barSource`.** `barFlagged:false` (or `barSource:"config"`) = a
   customer-declared bar (trust it). `barFlagged:true` (or `barSource:"default"`) = a fallback
   bar — render it dashed/amber "lower trust". Never compute or invent a bar.
5. **Provenance is one chip per datum, never fused.** `Class` strings literally read
   `"MEASURED ⋈ AUTHORED (joined, never fused)"` — render the MEASURED value and the AUTHORED
   `why`/`note` as two adjacent labelled values. The AUTHORED note/why/`corroborationWhy` is
   surfaced **verbatim** (with `author@version`) — never paraphrase it into UI prose.
6. **MCP double-parse + `McpLaneOff`** (gotcha #4 above) — a lane-off tool result is
   `{available:false,note}` inside `content[0].text`, `isError:false`. Not an error.
7. **No auth on `/api` or `/mcp`.** Bind to localhost / cluster-internal only. There is no
   `get_root_cause`/`get_cross_service`/`get_departures`/`get_topology` MCP tool by design —
   those surfaces are web-only (the AI must not be handed raw materials to author causation).
8. **Empty-state flags differ:** `CoverageView.available` / `SilenceLedgerView.available` /
   `EventsView.available` / `IncidentsView.available` go `false` when the lane/binding isn't
   ready (zeroed summary + note). `InsightsView` / `FindingsResponse` have no `available` flag —
   they just return empty arrays.

---

## 6. Build order (lowest-risk path)

1. Drop in `vigil-api-types.ts`; add the `/mcp` vite proxy; extend the fetch layer with
   `postJson` + the `callTool` MCP helper.
2. Wire the **7 unbuilt surfaces first** (they have zero coverage today, per frontend-spec
   §Appendix): root-cause-chain, departures, events, incidents, silence-ledger, referee,
   MCP-config.
3. Fix the **2 regressions** in existing surfaces: Timeline must map the live `projected[]`
   lane; CrossService must render `coverage_gaps` + the projected anticipatory chain.
4. Apply the provenance visual language (teal/violet-cone/gold-quote — already in
   `brand/tokens.css`) consistently; class-label Chat citations.
5. Adopt Cytoscape (topology + chain-path highlight) and ECharts (bands/timeline); move the
   `now` lane from 15 s poll → SSE.

See `artifacts/frontend-spec.md` for each page's KPIs, graphs, and operator actions.
