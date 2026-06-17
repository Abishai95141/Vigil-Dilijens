// The typed data layer. GET views poll on the 15s eval tick (via react-query);
// POST routes (validate-claim, chat, context-windows) and the MCP JSON-RPC
// endpoint have dedicated helpers. Everything is typed against ./types.ts.

import { type UseQueryResult, useQuery } from "@tanstack/react-query";
import type {
  ChatResponse,
  ClaimVerdict,
  ConfigView,
  CoverageView,
  CrossServiceView,
  DepartureView,
  EventsView,
  FindingsResponse,
  IncidentsView,
  InsightsView,
  McpInitializeResult,
  McpLaneOff,
  McpResponse,
  McpToolArguments,
  McpToolName,
  McpToolResult,
  McpToolsListResult,
  RootCauseChainView,
  SilenceLedgerView,
  TimelineView,
  TopologyView,
  UnexplainedView,
  WarningsView,
} from "./types";

export const POLL_MS = 15_000; // the obsd eval tick

async function getJson<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(path, { signal, headers: { accept: "application/json" } });
  if (!res.ok) throw new Error(`${path} → HTTP ${res.status}`);
  return (await res.json()) as T;
}

export async function postJson<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(path, {
    method: "POST",
    headers: { "content-type": "application/json", accept: "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`${path} → HTTP ${res.status}`);
  return (await res.json()) as T;
}

// One typed polling hook per GET view. react-query handles the 15s refetch, the
// keepPreviousData (no flicker), and the seq/ordering guard for us.
function useView<T>(key: string, path: string): UseQueryResult<T> {
  return useQuery<T>({
    queryKey: [key],
    queryFn: ({ signal }) => getJson<T>(path, signal),
    refetchInterval: POLL_MS,
    refetchOnWindowFocus: true,
    staleTime: POLL_MS - 1_000,
    placeholderData: (prev) => prev, // keep the last good view while refetching
    retry: 1,
  });
}

export const useCoverage = () => useView<CoverageView>("coverage", "/api/coverage");
export const useSilenceLedger = () =>
  useView<SilenceLedgerView>("silence-ledger", "/api/silence-ledger");
export const useFindings = () => useView<FindingsResponse>("findings", "/api/findings");
export const useInsights = () => useView<InsightsView>("insights", "/api/insights");
export const useTopology = () => useView<TopologyView>("topology", "/api/topology");
export const useCrossService = () =>
  useView<CrossServiceView>("cross-service", "/api/cross-service");
export const useRootCauseChain = () =>
  useView<RootCauseChainView>("root-cause-chain", "/api/root-cause-chain");
export const useDepartures = () => useView<DepartureView>("departures", "/api/departures");
export const useWarnings = () => useView<WarningsView>("warnings", "/api/warnings");
export const useEvents = () => useView<EventsView>("events", "/api/events");
export const useIncidents = () => useView<IncidentsView>("incidents", "/api/incidents");
export const useTimeline = () => useView<TimelineView>("timeline", "/api/timeline");
export const useConfig = () => useView<ConfigView>("config", "/api/config");
export const useUnexplained = () => useView<UnexplainedView>("unexplained", "/api/unexplained");

export const validateClaim = (claim: string) =>
  postJson<ClaimVerdict>("/api/validate-claim", { claim });
export const askChat = (question: string) => postJson<ChatResponse>("/api/chat", { question });

// ── MCP JSON-RPC client ──────────────────────────────────────────────────────
// One POST /mcp endpoint. A tools/call result wraps the view JSON as a STRING
// inside content[0].text — we double-parse it. A disabled lane returns
// {available:false,note} (McpLaneOff), isError:false — not an error.

let mcpId = 0;

export async function mcpInitialize(): Promise<McpInitializeResult> {
  const env = await postJson<McpResponse<McpInitializeResult>>("/mcp", {
    jsonrpc: "2.0",
    id: ++mcpId,
    method: "initialize",
    params: {
      protocolVersion: "2024-11-05",
      capabilities: {},
      clientInfo: { name: "vigil-console", version: "0.1.0" },
    },
  });
  if (env.error) throw new Error(env.error.message);
  return env.result!;
}

export async function mcpListTools(): Promise<McpToolsListResult> {
  const env = await postJson<McpResponse<McpToolsListResult>>("/mcp", {
    jsonrpc: "2.0",
    id: ++mcpId,
    method: "tools/list",
  });
  if (env.error) throw new Error(env.error.message);
  return env.result!;
}

export function isLaneOff(x: unknown): x is McpLaneOff {
  return !!x && typeof x === "object" && "available" in x && (x as McpLaneOff).available === false;
}

export async function mcpCall<T>(
  name: McpToolName,
  args?: McpToolArguments,
): Promise<T | McpLaneOff> {
  const env = await postJson<McpResponse<McpToolResult>>("/mcp", {
    jsonrpc: "2.0",
    id: ++mcpId,
    method: "tools/call",
    params: { name, arguments: args ?? {} },
  });
  if (env.error) throw new Error(env.error.message);
  // double-parse the view JSON out of content[0].text
  return JSON.parse(env.result!.content[0].text) as T | McpLaneOff;
}
