// Mirrors the Go json tags of the operator surfaces (doc 10 M2–M4):
//   obsd/internal/api/insights.go   → InsightsView
//   obsd/internal/api/topology.go   → TopologyView
//   obsd/internal/api/server.go     → UnexplainedView
//   obsd/internal/api/timeline.go   → TimelineView
// Drift becomes a visible bug; the proto contract makes it a compile error once
// the Connect client lands (techstack §10).

// --- /api/insights (the "now" surface, doc 10 §3.1) -------------------------

export interface InsightsView {
  clusterId: string;
  graphVersion: string;
  graphRelease: string;
  generatedAt: string;
  summary: InsightSummary;
  findings: InsightCard[];
  cascades: CascadeCard[];
}

export interface InsightSummary {
  total: number;
  full: number;
  degraded: number;
  entityLocal: number;
  firstOrder: number;
  secondOrder: number;
  cascades: number;
  withAtRisk: number;
}

export type Quality = "full" | "degraded";
export type Span = "entity-local" | "first-order" | "second-order";

export interface InsightCard {
  phenomenon: string;
  label: string;
  entityCei: string;
  namespace: string;
  name: string;
  kind: string;
  span: Span;
  quality: Quality;
  completeness: number;
  requiredMet: number;
  requiredTotal: number;
  members: MemberRow[];
  unobservable: string[];
  spanPath: EdgeStepRow[];
  suspectEdges: string[];
  blastRadius: AtRiskRow[];
  graphVersion: string;
}

export interface MemberRow {
  metric: string;
  role: string;
  temporal: string;
  state: string;
  sampleAt: string;
  barFlagged: boolean;
  note: string;
  neighbour: string;
  via: string;
  hop: number;
  edgeResult: string;
}

export interface EdgeStepRow {
  type: string;
  from: string;
  to: string;
  result: string;
}

export interface AtRiskRow {
  ceiKey: string;
  phenomenon: string;
  related: string;
  temporal: string;
  why: string;
}

export interface CascadeCard {
  trigger: FindingRef;
  downstream: FindingRef;
  why: string;
  temporal: string;
  related: string;
}

export interface FindingRef {
  phenomenon: string;
  entityCei: string;
  evaluatedAt: string;
  quality: string;
}

// --- /api/topology (doc 10 §3.2) --------------------------------------------

export interface TopologyView {
  clusterId: string;
  graphVersion: string;
  generatedAt: string;
  nodes: TopoNode[];
  edges: TopoEdge[];
  summary: TopologySummary;
  truncated: number;
}

export interface TopologySummary {
  nodes: number;
  edges: number;
  validEdges: number;
  suspectEdges: number;
  matched: number;
  loud: number;
  selected: number;
  warned: number;
}

export interface TopoNode {
  ceiKey: string;
  kind: string;
  namespace: string;
  name: string;
  selected: boolean;
  matched: boolean;
  degraded: boolean;
  loud: boolean;
  warned: boolean;
  phenomena: string[];
}

export type EdgeStatus = "valid" | "suspect" | "retracted";

export interface TopoEdge {
  type: string;
  from: string;
  to: string;
  status: EdgeStatus;
}

// --- /api/unexplained (doc 08 / 10 §3.1) ------------------------------------

export interface UnexplainedView {
  generatedAt: string;
  graphVersion: string;
  openCards: UnexplainedCard[];
  candidates: CandidateReport[];
  blindSpot: string;
}

export type UnexplainedStatus =
  | "new"
  | "aging"
  | "superseded-by-match"
  | "resolved";

export interface UnexplainedCard {
  scope: string;
  namespace: string;
  name: string;
  kind: string;
  loudStates: LoudState[];
  matchCheck: string;
  status: UnexplainedStatus;
  mark: string;
  firstSeen: string;
  lastSeen: string;
  occurrences: number;
  supersededBy: string;
  graphVersion: string;
}

export interface LoudState {
  metric: string;
  kind: string;
  state: string;
  barSource: string;
  flagged: boolean;
  sampleAt: string;
}

export interface CandidateReport {
  metrics: string[];
  entityKind: string;
  windows: number;
  entities: string[];
  firstSeen: string;
  lastSeen: string;
  rationale: string;
  graphVersion: string;
}

// --- /api/timeline (doc 10 §3.3) --------------------------------------------

export interface TimelineView {
  generatedAt: string;
  window: { from: string; to: string };
  matches: TimelineSpan[];
  unexplained: TimelineSpan[];
  projected: TimelineSpan[];
  projectedNote: string;
}

export interface TimelineSpan {
  class: "MEASURED" | "PROJECTED";
  surface: string;
  label: string;
  entityCei: string;
  name: string;
  kind: string;
  status: string;
  from: string;
  to: string;
}

// entityLabel renders a CEI key as ns/name (kind) for display.
export function entityLabel(ceiKey: string): string {
  const parts = ceiKey.split("|");
  if (parts.length === 6 && parts[0] === "i") {
    const [, , ns, kind, name] = parts;
    return ns ? `${ns}/${name} (${kind})` : `${name} (${kind})`;
  }
  return ceiKey;
}

// shortMetric trims a long metric name for chips.
export function shortMetric(m: string): string {
  return m
    .replace(/^container_/, "")
    .replace(/^node_/, "")
    .replace(/_total$/, "")
    .replace(/_bytes$/, "");
}

// --- Early warnings (doc 10 M5 / 09 M4) — mirrors obsd/internal/api/warnings.go

export interface WarningsView {
  class: string; // PROJECTED
  generatedAt: string;
  graphVersion: string;
  graphRelease: string;
  enabled: boolean;
  gateNote: string;
  warnings: WarningCard[];
  silences: SilenceRow[];
  unbudgeted: number;
  clock: ClockHealthRow;
}

export interface WarningCard {
  class: string;
  isProjection: boolean;
  entityCei: string;
  namespace: string;
  name: string;
  kind: string;
  metric: string;
  seriesKind: string;
  barValue: number;
  barUnit: string;
  barSource: string;
  barFlagged: boolean;
  direction: string;
  basisAt: string;
  crossAt: string;
  earliestAt: string;
  latestAt: string;
  latestBeyondHorizon: boolean;
  timeToCrossSeconds: number;
  confidence: string; // tight | moderate | wide
  precursorPhenomena: string[];
  atRisk: AtRiskRow[];
  contextPoints: number;
  horizonSteps: number;
  cadenceSeconds: number;
  graphVersion: string;
}

export interface SilenceRow {
  entityCei: string;
  metric: string;
  reason: string;
}

export interface ClockHealthRow {
  ready: boolean;
  statusCode: number;
  degradedSince: string;
}

/** Render a duration in seconds as a short human span ("~13 min"). */
export function shortSpan(seconds: number): string {
  if (seconds < 90) return `${Math.round(seconds)} s`;
  if (seconds < 5400) return `${Math.round(seconds / 60)} min`;
  return `${(seconds / 3600).toFixed(1)} h`;
}
