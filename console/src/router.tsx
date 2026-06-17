import { createRootRoute, createRoute, createRouter } from "@tanstack/react-router";
import { Shell } from "./app/Shell";
import { GraphPage, OverviewPage, Stub } from "./routes/pages";

const root = createRootRoute({ component: Shell });

function r(path: string, component: () => React.ReactNode) {
  return createRoute({ getParentRoute: () => root, path, component });
}

// Stubs are honest placeholders — they name what the surface will show, never an
// empty page. They are replaced one at a time by the real surfaces.
const routeTree = root.addChildren([
  r("/", GraphPage),
  r("/overview", OverviewPage),
  r("/insights", () => (
    <Stub
      num="03"
      title="Insights"
      lede="The why-feed: current MEASURED phenomenon matches with their AUTHORED notes, and the cascade stories that connect them."
      note="Renders /api/insights — finding cards with member state + the authored note (the only 'why'), span/blast-radius, and cascade trigger→downstream stories."
    />
  )),
  r("/root-cause", () => (
    <Stub
      num="04"
      title="Root cause & cascades"
      lede="The transitive root-cause chain and the cross-service cascade — MEASURED edges, AUTHORED orientation, joined never fused."
      note="Renders /api/root-cause-chain + /api/cross-service as a directed graph: teal MEASURED nodes, the authored 'why' on each edge, silent-intermediate gaps shown, and the gate-pending projected ripple."
    />
  )),
  r("/events", () => (
    <Stub
      num="05"
      title="Events"
      lede="Discrete Kubernetes events (OOMKilled, CrashLoopBackOff) as MEASURED findings, joined to a gauge phenomenon by role — never fused."
      note="Renders /api/events — standalone vs corroborated split, role resolution, and the authored corroboration note when present."
    />
  )),
  r("/forecast", () => (
    <Stub
      num="06"
      title="Early warnings"
      lede="The Tier-B forecast: what is projected to cross a config bar soon. A band that never collapses to a line."
      note="Renders /api/warnings — a widening violet uncertainty cone bounded by earliest/latest around the cross point, the bar line, register fixed to 'projected to cross', plus the lane's silence accounting."
    />
  )),
  r("/anomalies", () => (
    <Stub
      num="07"
      title="Anomalies"
      lede="Three honest notions: capacity-crossing (MEASURED), band-departure (PROJECTED, gate-pending), and the unexplained blind spot."
      note="A unified inbox over /api/findings, /api/departures, and /api/unexplained — one provenance chip per row, never an anomaly score; the gate-pending lane renders its note, not an empty page."
    />
  )),
  r("/incidents", () => (
    <Stub
      num="08"
      title="Incidents"
      lede="Durable cross-run recurrence: has this happened before, and how often."
      note="Renders /api/incidents — recurrence count, lifespan, first/last seen per role; a deterministic grouping, never a cause."
    />
  )),
  r("/timeline", () => (
    <Stub
      num="09"
      title="Timeline"
      lede="Matches, unexplained aging spans, and the projected future lane over time."
      note="Renders /api/timeline — MEASURED match + unexplained lanes and the live PROJECTED lane."
    />
  )),
  r("/coverage", () => (
    <Stub
      num="10"
      title="Coverage"
      lede="What Vigil can and cannot watch on this cluster — the charter floor, made visible."
      note="Renders /api/coverage — resolvability, configBound vs defaultBars vs unbounded, per-phenomenon observability, and the threshold-rule binding table."
    />
  )),
  r("/silence", () => (
    <Stub
      num="11"
      title="Silence ledger"
      lede="The deterministic-absence matrix: every (entity, variable) pair is watched, or silent with a verbatim reason."
      note="Renders /api/silence-ledger as an entity×variable heatmap (watched / unbounded / no-stream-key / unresolved / out-of-scope) with the completeness invariant totalPairs = watched + silent."
    />
  )),
  r("/referee", () => (
    <Stub
      num="12"
      title="Referee"
      lede="Paste a claim; see the charter verdict. Flags fabricated causation, rescues authored relations, never blocks."
      note="Posts to /api/validate-claim — renders each reason's class (generated-causation / class-fusion / future-certainty), matchedAuthored, advisory-not-blocking."
    />
  )),
  r("/config", () => (
    <Stub
      num="13"
      title="Config"
      lede="Runtime configuration and the forecast lane's gate posture."
      note="Renders /api/config — cluster/profile/params/graph, scrape + eval cadence, Tier-B budget, and the forecast gate (the honest reason warnings can be off)."
    />
  )),
  r("/mcp", () => (
    <Stub
      num="14"
      title="MCP / Integrations"
      lede="Connect an AI to Vigil's read-only harness. Copy the config, test the connection, see the tool catalog."
      note="Connection status (POST /mcp initialize), copy-pasteable client config, the 7-tool catalog with provenance chips, a test-connection round-trip, and the deliberate no-root-cause-tool guarantee."
    />
  )),
  r("/chat", () => (
    <Stub
      num="15"
      title="Ask Vigil"
      lede="A grounded assistant. It cites its sources by provenance class and never improvises a cause."
      note="Posts to /api/chat — answers with class-labelled citations, refusals shown first-class."
    />
  )),
]);

export const router = createRouter({ routeTree, defaultPreload: "intent" });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
