import { createRootRoute, createRoute, createRouter } from "@tanstack/react-router";
import { Shell } from "./app/Shell";
import { AnomaliesPage } from "./routes/anomalies";
import { CausalHypothesesPage } from "./routes/causal-hypotheses";
import { ConfigPage } from "./routes/config";
import { CoveragePage } from "./routes/coverage";
import { EventsPage } from "./routes/events";
import { ForecastPage } from "./routes/forecast";
import { GovernancePage } from "./routes/governance";
import { IncidentsPage } from "./routes/incidents";
import { InsightsPage } from "./routes/insights";
import { McpPage } from "./routes/mcp";
import { GraphPage, OverviewPage } from "./routes/pages";
import { RefereePage } from "./routes/referee";
import { RightSizingPage } from "./routes/right-sizing";
import { RootCausePage } from "./routes/rootcause";
import { SilencePage } from "./routes/silence";
import { TimelinePage } from "./routes/timeline";

const root = createRootRoute({ component: Shell });

function r(path: string, component: () => React.ReactNode) {
  return createRoute({ getParentRoute: () => root, path, component });
}

// Every surface is now real and bound to its /api endpoint. The graph is the home.
const routeTree = root.addChildren([
  r("/", GraphPage),
  r("/overview", OverviewPage),
  r("/insights", InsightsPage),
  r("/root-cause", RootCausePage),
  r("/events", EventsPage),
  r("/forecast", ForecastPage),
  r("/anomalies", AnomaliesPage),
  r("/incidents", IncidentsPage),
  r("/timeline", TimelinePage),
  r("/coverage", CoveragePage),
  r("/silence", SilencePage),
  r("/governance", GovernancePage),
  r("/causal-hypotheses", CausalHypothesesPage),
  r("/right-sizing", RightSizingPage),
  r("/referee", RefereePage),
  r("/config", ConfigPage),
  // NOTE: not "/mcp" — that path is proxied to obsd's POST-only JSON-RPC endpoint
  // (a direct GET there returns 405). The page lives at /integrations; the MCP
  // client still POSTs to /mcp via the proxy.
  r("/integrations", McpPage),
]);

export const router = createRouter({ routeTree, defaultPreload: "intent" });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
