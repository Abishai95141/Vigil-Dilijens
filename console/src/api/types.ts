// =============================================================================
// vigil-api-types.ts — the complete Vigil obsd wire contract (frontend client).
// GENERATED from the Go view structs (obsd/internal/api/*.go, internal/flow/chain.go,
// internal/mcp/*.go) — field names match the json: tags 1:1.
//
// CONVENTIONS
//  - All /api/* views are camelCase. EXCEPTION: the embedded flow.Chain
//    (/api/cross-service, /api/root-cause-chain) is snake_case — its keys are
//    quoted (e.g. "most_upstream_degraded_node"). Handle BOTH in one payload.
//  - time.Time -> string (RFC3339 UTC). float64/int -> number. bool -> boolean.
//  - Slices may serialize as null when empty on some views (typed `T[] | null`);
//    most builders initialize to []. Optional (omitempty) fields use `?`.
//  - PROVENANCE is the load-bearing axis: each view/field is tagged MEASURED |
//    PROJECTED | AUTHORED | un-classed. Never merge classes in the UI.
//  - Lane shape: a gate-pending/off lane returns {enabled|active:false, note/gateNote}
//    with the data array absent/empty — render the note, never an empty panel.
// =============================================================================


// ╔══ GROUP 1 — coverage · silence-ledger · insights · findings ══╗

// ============================================================================
// Vigil obsd wire contract — group truth-1
// Source of truth: obsd/internal/api/{coverage,silence,insights,server}.go +
//                  obsd/internal/store/findings.go  (Go json: tags, 1:1)
// All keys camelCase (the snake_case flow.Chain exception is NOT in this group).
// ============================================================================

// ── /api/coverage ───────────────────────────────────────────────────────────

// MEASURED (about the system's OWN coverage) — GET /api/coverage
// The honest map of what Vigil can/cannot watch on this cluster. Every
// (entity,variable) pair has a visible state. available=false => binding not
// compiled yet (honest empty state: summary zeroed, slices empty, caveats set).
// Source: obsd/internal/api/coverage.go CoverageView
export interface CoverageView {
  clusterId: string;
  graphVersion: string;
  graphRelease: string; // human release name (e.g. "v0.1.0"); "" = unreleased dev build
  generatedAt: string;  // RFC3339 (UTC)
  available: boolean;   // false => binding not yet compiled
  summary: CoverageSummary;
  phenomena: PhenomenonRow[];
  rules: RuleRow[];
  selection: SelectionSummary;
  unbounded: string[];  // bar-less pairs: Tier-B-ineligible, listed
  caveats: string[];    // standing honesty notes (pending milestones)
}

// MEASURED — headline rollup. Source: coverage.go CoverageSummary
export interface CoverageSummary {
  entities: number;
  tierA: number;
  resolvability: number;   // float64 fraction
  configBound: number;
  configEligible: number;
  defaultBars: number;
  phenomenaFull: number;
  phenomenaPartial: number;
  phenomenaNone: number;
  qaVerified: number;
  qaSuspect: number;
  qaFailed: number;
}

// MEASURED — one phenomenon's observability on this cluster.
// Source: coverage.go PhenomenonRow
export interface PhenomenonRow {
  id: string;
  label: string;
  severity?: string;         // AUTHORED harm level (doc 21 Phase 5): critical|high|medium|low; absent = undeclared
  observability: string;     // "full" | "partial" | "none"
  requiredTotal: number;
  requiredObservable: number; // NOTE: Go field RequiredOk, json tag "requiredObservable"
  missingReasons: string[];
}

// MEASURED — one threshold rule's binding coverage. Source: coverage.go RuleRow
export interface RuleRow {
  ruleId: string;
  kind: string;
  entityScope: string;
  instantiated: number;
  configBound: number;
  defaultBound: number; // flagged (lower trust)
  unbounded: number;
  outOfScope: number;
  unresolved: number;
}

// MEASURED — attention rollup. Source: coverage.go SelectionSummary
export interface SelectionSummary {
  tierA: number;
  noneByReason: Record<string, number>; // keyed by selection reason code
}

// ── /api/silence-ledger ─────────────────────────────────────────────────────

// MEASURED (about the system's OWN coverage) — GET /api/silence-ledger
// Deterministic ABSENCE: every (entity,variable) pair is WATCHED or listed here
// SILENT with a verbatim reason. available=false => binding not compiled
// (honest empty state: note carries the "connect a cluster" string).
// Source: obsd/internal/api/silence.go SilenceLedgerView
export interface SilenceLedgerView {
  class: string;        // always "MEASURED" — about own coverage
  generatedAt: string;  // RFC3339 (UTC)
  graphVersion: string;
  graphRelease: string;
  available: boolean;   // false => binding not compiled
  summary: SilenceLedgerSummary;
  silent: SilenceLedgerRow[];
  note: string;
}

// MEASURED — headline rollup. byReason keyed by silence class
// ("unbounded" | "no-stream-key" | "unresolved" | "out-of-scope").
// Completeness invariant: totalPairs === watched + silent.
// Source: silence.go SilenceLedgerSummary
export interface SilenceLedgerSummary {
  totalPairs: number;
  watched: number;
  silent: number;
  byReason: Record<string, number>;
}

// MEASURED — one (entity,variable) pair that is NOT watched, with its verbatim
// compiler reason (never a generated explanation).
// Source: silence.go SilenceLedgerRow
export interface SilenceLedgerRow {
  entityCei: string;
  roleKey?: string;     // omitempty
  entity: string;       // "Container" | "Pod" | "Node" | "PVC"
  container?: string;   // omitempty
  ruleId: string;
  metric: string;
  state: string;        // "bound" | "unresolved" | "out-of-scope"
  reasonClass: string;  // "unbounded" | "no-stream-key" | "unresolved" | "out-of-scope"
  reason: string;       // the binding's verbatim honest reason
}

// ── /api/insights ───────────────────────────────────────────────────────────

// MEASURED matches + ADJACENT AUTHORED notes (join, never fuse) — GET /api/insights
// The "now" surface: phenomena currently matched, each with its MEASURED evidence
// trail and AUTHORED member/relation notes attached side-by-side, plus recognized
// cascade stories. Live snapshot, published per tick.
// Source: obsd/internal/api/insights.go InsightsView
export interface InsightsView {
  clusterId: string;
  graphVersion: string;
  graphRelease: string;
  generatedAt: string;  // RFC3339 (UTC)
  summary: InsightSummary;
  findings: InsightCard[];
  cascades: CascadeCard[];
}

// MEASURED — headline rollup of current findings. Source: insights.go InsightSummary
export interface InsightSummary {
  total: number;
  full: number;
  degraded: number;
  entityLocal: number;
  firstOrder: number;
  secondOrder: number;
  cascades: number;
  withAtRisk: number; // findings carrying a blast radius
}

// MEASURED match with AUTHORED notes adjacent (never paraphrased into causation).
// Source: insights.go InsightCard
export interface InsightCard {
  phenomenon: string;
  label: string;
  entityCei: string;
  namespace: string;
  name: string;
  kind: string;
  span: string;          // "entity-local" | "first-order" | "second-order"
  quality: string;
  completeness: number;  // float64 fraction
  requiredMet: number;
  requiredTotal: number;
  members: MemberRow[];        // MEASURED evidence trail, each with its AUTHORED note
  unobservable: string[];      // required members that could not be checked here
  spanPath: EdgeStepRow[];     // edges crossed, with validity verdicts (always [], never null)
  suspectEdges: string[];      // edges whose staleness degrades the match
  blastRadius: AtRiskRow[];    // AUTHORED downstream relations made concrete (always [], never null)
  graphVersion: string;
}

// MEASURED state + AUTHORED note kept as a labelled, separate field.
// Source: insights.go MemberRow
export interface MemberRow {
  metric: string;
  role: string;        // "required" | "supporting" | "corroborating"
  temporal: string;    // "T0-" | "T0" | "T0+..."
  state: string;
  sampleAt: string;    // RFC3339 (UTC)
  barFlagged: boolean; // a default-sourced bar (lower trust), surfaced
  note: string;        // AUTHORED member note (the only "why", attributed)
  neighbour: string;   // cross-entity evidence (first/second-order): the CEI it crossed to
  via: string;         // edge type(s) crossed
  hop: number;         // 1 = neighbour, 2 = two-hop (0 = on the anchor)
  edgeResult: string;  // "valid" | "suspect"
}

// MEASURED — one edge in a span's derivation, with its validity verdict.
// Source: insights.go EdgeStepRow
export interface EdgeStepRow {
  type: string;
  from: string;
  to: string;
  result: string; // "valid" | "suspect"
}

// AUTHORED — one blast-radius entry: an authored downstream relation made
// concrete on the current topology, never a prediction. Source: insights.go AtRiskRow
export interface AtRiskRow {
  ceiKey: string;
  phenomenon: string;
  related: string;  // "same-entity" | "<edge-type>"
  temporal: string;
  why: string;      // AUTHORED relation note, verbatim
}

// MEASURED co-occurrences joined by an AUTHORED relation (adjacent, never causal
// proof). Source: insights.go CascadeCard
export interface CascadeCard {
  trigger: FindingRef;
  downstream: FindingRef;
  why: string;      // AUTHORED relation note
  temporal: string; // authored temporal tag
  related: string;  // "same-entity" | "<edge-type>"
}

// MEASURED — names one finding occurrence in a cascade story. Source: insights.go FindingRef
export interface FindingRef {
  phenomenon: string;
  entityCei: string;
  evaluatedAt: string; // RFC3339 (UTC)
  quality: string;
}

// ── /api/findings ───────────────────────────────────────────────────────────

// un-classed envelope — GET /api/findings
// NOTE: this route serves store.FindingRow (the raw persisted rows), NOT the
// api.InsightCard above. Wrapped in the response envelope below.
// Source: obsd/internal/api/server.go findingsResponse
export interface FindingsResponse {
  generatedAt: string;     // RFC3339 (UTC); absent only on the nil-provider early-return
  findings: FindingRow[];
}

// MEASURED match (persisted, durable). stale + lastSeenAgoSeconds are DERIVED at
// serve time (LastSeen vs response stamp) — a row may be old; stale=true means
// "last seen Xs ago", NOT firing-now. Members/Unobservable are json:"-" (NOT on
// the wire). Source: obsd/internal/store/findings.go FindingRow
export interface FindingRow {
  phenomenon: string;
  label: string;
  entityCei: string;
  namespace: string;
  name: string;
  kind: string;
  quality: string;
  completeness: number;       // float64 fraction
  requiredTotal: number;
  requiredMet: number;
  requiredUnobserved: number; // count (NOT the string[] that InsightCard.unobservable carries)
  graphVersion: string;
  firstSeen: string;          // RFC3339 (UTC)
  lastSeen: string;           // RFC3339 (UTC)
  stale: boolean;             // DERIVED at serve time; false if FindingsStaleAfter<=0
  lastSeenAgoSeconds: number; // DERIVED at serve time; 0 if lastSeen >= now
  // NOTE: Go fields Members []byte / Unobservable []byte are tagged json:"-" and
  // are intentionally absent from the wire JSON.
}


// ╔══ GROUP 2 — topology · cross-service · root-cause-chain (+ snake_case flow.Chain) ══╗

// ============================================================================
// GROUP: graph  —  /api/topology, /api/cross-service, /api/root-cause-chain
// Field names match Go json tags 1:1. The api/* views are camelCase; the
// embedded flow.Chain (obsd/internal/flow/chain.go) is snake_case (quoted keys).
// ============================================================================

// ---------------------------------------------------------------------------
// Route: GET /api/topology
// Provenance: MEASURED (current-condition marks; predictive marks are a separate
//   visual language and are NOT in this payload — `warned` is the only "might" glyph).
// Source: obsd/internal/api/topology.go (TopologyView)
// ---------------------------------------------------------------------------
export interface TopologyView {
  clusterId: string;
  graphVersion: string;
  generatedAt: string;          // time.Time -> RFC3339 UTC
  nodes: TopoNode[];
  edges: TopoEdge[];
  summary: TopologySummary;
  truncated: number;            // entities omitted past the cap (stated, never silent)
}

// MEASURED — headline rollup. /api/topology
export interface TopologySummary {
  nodes: number;
  edges: number;
  validEdges: number;
  suspectEdges: number;
  matched: number;              // nodes carrying a current phenomenon match
  loud: number;                 // nodes carrying an unexplained loud card
  selected: number;             // Tier-A nodes
  warned: number;               // nodes carrying a PROJECTED early warning (separate glyph family)
}

// MEASURED — one entity; marks are labelled booleans (distinct glyphs). /api/topology
export interface TopoNode {
  ceiKey: string;
  kind: string;
  namespace: string;
  name: string;
  layer?: string;               // workload | node | service | storage — for view-mode filtering (omitempty)
  replicas?: number;            // pods rolled into this workload (omitempty; absent for Node/Service/PVC)
  selected: boolean;            // Tier-A (doc 06)
  matched: boolean;             // a current phenomenon match (doc 07) — "is"
  degraded: boolean;            // the match(es) here are degraded
  loud: boolean;                // an unexplained loud card (doc 08)
  warned: boolean;              // an early-warning target (doc 09) — "might", separate visual language
  phenomena: string[];          // matched phenomenon ids on this node (never null — emitted as [])
}

// MEASURED — one topology edge with its validity verdict at `now`. /api/topology
export interface TopoEdge {
  type: string;
  from: string;
  to: string;
  status: string;               // "valid" | "suspect" | "retracted"
}

// ---------------------------------------------------------------------------
// Route: GET /api/cross-service
// Provenance: MEASURED ⋈ AUTHORED (joined, never fused) for the gate-passed lane;
//   the projected* lane is PROJECTED (gate-pending — withheld until its gate passes).
// Lane shape: {enabled, active, note} for MEASURED; {projectedActive, projectedNote}
//   for PROJECTED. `chain`/`projectedChain` are omitempty (absent when OFF/quiet/pending).
// Source: obsd/internal/api/crossservice.go (CrossServiceView)
// ---------------------------------------------------------------------------
export interface CrossServiceView {
  generatedAt: string;          // time.Time -> RFC3339
  class: string;                // "MEASURED ⋈ AUTHORED (joined, never fused)"
  enabled: boolean;             // flow discovery (doc 15) is running
  active: boolean;              // a cross-service cascade is firing this tick
  note: string;                 // honest lane state (OFF | quiet | active)
  gateNote: string;             // the doc 11 §3.5 gate posture
  chain?: Chain;                // omitempty — present only when active (MEASURED ⋈ AUTHORED)
  // --- Phase E: the ANTICIPATORY (PROJECTED) lane, gated separately ---
  projectedActive: boolean;     // stays false while gate-pending
  projectedNote: string;        // OFF | gate-pending | quiet | active
  projectedChain?: Chain;       // omitempty — withheld until its backtest gate passes
}

// ---------------------------------------------------------------------------
// Route: GET /api/root-cause-chain
// Provenance: MEASURED ⋈ AUTHORED (joined, never fused) for the transitive lane;
//   the projected* lane is PROJECTED (gate-pending).
// Lane shape: {enabled, active, note} (MEASURED) + {projectedActive, projectedNote}
//   (PROJECTED). `chains`/`projectedChains` are omitempty arrays.
// Source: obsd/internal/api/rootcause.go (RootCauseChainView)
// ---------------------------------------------------------------------------
export interface RootCauseChainView {
  generatedAt: string;          // time.Time -> RFC3339
  class: string;                // "MEASURED ⋈ AUTHORED (joined, never fused)"
  enabled: boolean;             // flow discovery (doc 15) is running
  active: boolean;              // at least one transitive chain is firing this tick
  note: string;                 // honest lane state
  chains?: Chain[];             // omitempty — present only when active
  // --- doc 15 cap. D: the multi-hop PROJECTED cascade, gated separately ---
  projectedActive: boolean;     // stays false while gate-pending
  projectedNote: string;        // OFF | gate-pending | quiet | active
  projectedChains?: Chain[];    // omitempty — withheld until its real 2-hop gate passes
}

// ===========================================================================
// flow.Chain and its nested types — SNAKE_CASE keys (quoted).
// Source: obsd/internal/flow/chain.go
// Embedded by CrossServiceView.chain/projectedChain and
//   RootCauseChainView.chains[]/projectedChains[].
// Provenance: a JOIN of MEASURED observed-flow edge + AUTHORED "why" + MEASURED
//   structural position; the projected fields (root_band / path[].band) are PROJECTED.
// ===========================================================================
export interface Chain {
  "most_upstream_degraded_node": string;  // MEASURED structural position (not a causal claim)
  "node_class": string;                   // "MEASURED (structural fan-in over observed-flow edges)"
  "node_basis": string;
  "chain": Link[];                        // NOTE: the Links field serializes under json key "chain"
  "symptoms": SymptomOut[];
  "coverage_gaps": CoverageOut;
  "generated_at": string;                 // time.Time -> RFC3339
  "path"?: PathStep[];                     // omitempty — the ORDERED transitive chain (cap. B); empty for one-hop
  "gaps"?: ChainGap[];                     // omitempty — honest breaks (silent intermediates / hop ceilings)
  "root_band"?: ProjectedBand;             // omitempty — PROJECTED; the forecast root's own band (cap. D), null for MEASURED
}

// One impacted-caller ← degraded-callee pairing over an observed-flow edge.
// MEASURED edge + AUTHORED why, joined. flow.Chain (snake_case).
export interface Link {
  "impacted": string;            // namespace/workload of the caller
  "degraded": string;            // namespace/workload of the degraded callee
  "edge_class": string;          // "MEASURED observed flow"
  "service_ports": number[];     // the dialed service ports
  "conn_depth": number;          // MEASURED conntrack depth on this edge (caller-side)
  "edge_traversal": string;      // "valid" | "suspect"
  "why": string;                 // verbatim AUTHORED note
  "why_class": string;           // "AUTHORED"
  "temporal": string;            // "T0->T0+"
  "author": string;
  "version": string;
}

// One ORDERED impact edge in a transitive root-cause chain (cap. B).
// MEASURED edge + AUTHORED why; `band` is PROJECTED (cap. D only). flow.Chain (snake_case).
export interface PathStep {
  "hop": number;                 // 1-based BFS depth from the chain root
  "upstream": string;            // ns/workload — root-ward degraded node (callee)
  "upstream_phenomenon": string; // its OWN MEASURED finding
  "downstream": string;          // ns/workload — impacted degraded node (caller)
  "downstream_phenomenon": string; // its OWN MEASURED finding
  "edge_class": string;          // "MEASURED observed flow"
  "edge_traversal": string;      // "valid" | "suspect"
  "why": string;                 // verbatim AUTHORED relation note
  "why_class": string;           // "AUTHORED"
  "temporal": string;
  "author": string;
  "version": string;
  "band"?: ProjectedBand;        // omitempty — PROJECTED downstream-impact window (cap. D); null for MEASURED
}

// PROJECTED — one downstream node's inherited, WIDENED projected-impact window (cap. D).
// flow.Chain (snake_case). Band never collapses to a line.
export interface ProjectedBand {
  "class": string;               // "PROJECTED"
  "earliest": string;            // band lower edge, RFC3339 UTC
  "latest": string;              // band upper edge, RFC3339 UTC, or "" when open
  "open": boolean;               // far edge is open (beyond horizon, or zero-width root)
  "hops_from_root": number;      // 0 = the root itself; >=1 = inherited+widened
  "root_metric": string;         // the forecast root's series (e.g. working_set)
  "root_cross_at": string;       // the root's point projection (shown only WITH the band)
  "confidence": string;          // the root forecast's confidence, verbatim
  "widen_note": string;          // the stated per-hop widening assumption
}

// A stated break in the asserted chain (cap. B §2) — never silently bridged.
// flow.Chain (snake_case).
export interface ChainGap {
  "from": string;                // ns/workload — degraded node whose asserted chain ends here
  "to": string;                  // ns/workload — the SILENT flow-adjacent node, or "" for a hop ceiling
  "reason": string;              // the honest explanation
}

// One MEASURED finding fed to the walk, with its provenance class. flow.Chain (snake_case).
export interface SymptomOut {
  "workload": string;
  "phenomenon": string;
  "class": string;               // "MEASURED" (or "SYNTHETIC" for a unit-test seed)
  "detail": string;              // the measured basis, e.g. "cpu throttle ratio 0.42 > 0.25 bar"
}

// MEASURED — honest recovery accounting, surfaced as first-class gaps. flow.Chain (snake_case).
export interface CoverageOut {
  "resolvable_flows": number;
  "snat_masked_flows": number;
  "unresolved_flows": number;
  "infra_flows": number;
  "unreplied_flows": number;
  "snapshots": number;
}


// ╔══ GROUP 3 — warnings · departures · timeline · context-windows ══╗

// ============================================================================
// GROUP: forecast-anomaly
// Source of truth: obsd/internal/api/*.go json tags (read directly, not live).
// All views here are camelCase (the snake_case exception is flow.Chain only,
// which is NOT in this group).
// ============================================================================


// ---------------------------------------------------------------------------
// GET /api/warnings  ->  WarningsView
// Provenance: PROJECTED (the early-warning lane). Nested AtRiskRow is AUTHORED,
// ClockHealthRow is MEASURED — surfaced adjacently (join, never fused).
// Source: obsd/internal/api/warnings.go
// OFF pattern: enabled=false => layer gated dark, gateNote states why,
// warnings/silences are empty arrays.
// ---------------------------------------------------------------------------
export interface WarningsView {
  class: string;          // constant "PROJECTED" — the lane's provenance class
  generatedAt: string;    // RFC3339
  graphVersion: string;
  graphRelease: string;
  enabled: boolean;       // false => forecasting layer is OFF (gate rule)
  gateNote: string;       // why the lane is dark when disabled ("" when enabled)
  warnings: WarningCard[];   // always present (may be [])
  silences: SilenceRow[];    // always present (may be [])
  unbudgeted: number;        // eligible-but-unbudgeted count
  clock: ClockHealthRow;
}

// One early-warning candidate. PROJECTED projection + AUTHORED references,
// labelled, never fused. Source: obsd/internal/api/warnings.go (WarningCard).
export interface WarningCard {
  class: string;          // "PROJECTED"
  isProjection: boolean;  // mandatory mark

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

  // The projection — always with its band.
  basisAt: string;              // RFC3339
  crossAt: string;              // RFC3339
  earliestAt: string;           // RFC3339
  latestAt: string;             // RFC3339
  latestBeyondHorizon: boolean; // band's far edge is OPEN (crossing may not happen)
  timeToCrossSeconds: number;   // from a Go Duration.Seconds() — float seconds
  confidence: string;

  // Aging: held briefly for stability; card shows the LAST real projection (basisAt).
  aging: boolean;
  firstSeenAt: string;          // RFC3339, stable across refreshes

  // AUTHORED references, cited verbatim — never paraphrased into a causal sentence.
  precursorPhenomena: string[]; // always present (may be [])
  atRisk: AtRiskRow[];          // blast radius per the graph; always present (may be [])

  // Decomposition record (v1).
  contextPoints: number;
  horizonSteps: number;
  cadenceSeconds: number;       // from a Go Duration.Seconds() — float seconds
  graphVersion: string;
}

// One quiet target with its guardrail reason. Provenance: MEASURED (audit trail).
// Source: obsd/internal/api/warnings.go (SilenceRow).
export interface SilenceRow {
  entityCei: string;
  metric: string;
  reason: string;
}

// Tier-B clock degradation panel source. Provenance: MEASURED.
// Source: obsd/internal/api/warnings.go (ClockHealthRow).
export interface ClockHealthRow {
  ready: boolean;
  statusCode: number;       // uint32
  degradedSince: string;    // RFC3339; zero ("0001-01-01T00:00:00Z") when healthy
}

// One blast-radius entry. Provenance: AUTHORED (downstream relation made concrete
// on current topology; `why` is a verbatim authored note). Nested in WarningCard.
// Source: obsd/internal/api/insights.go (AtRiskRow).


// ---------------------------------------------------------------------------
// GET /api/departures  ->  DepartureView
// Provenance: PROJECTED (band-departure anomaly: a MEASURED sample that left its
// own PROJECTED forecast band — joined, never fused). Source: obsd/internal/api/departure.go
// Gate-pending pattern: {enabled, active, note}. departures is present (active=true)
// only when enabled && gatePassed && a sample departed; otherwise omitted, active=false.
// ---------------------------------------------------------------------------
export interface DepartureView {
  generatedAt: string;     // RFC3339
  class: string;           // "PROJECTED band ⋈ MEASURED sample (joined, never fused)"
  enabled: boolean;        // --departure-enabled is set
  active: boolean;         // a departure is surfaced this tick (only once gate-passed)
  note: string;            // honest lane state (off | pending | quiet | active)
  departures?: Departure[]; // omitempty — present only when active=true
}

// doc 22 C3 — direction-free causal hypotheses: coupled series that co-stepped,
// surfaced for a human to author the DIRECTION (the system never infers it).
// Source: obsd/internal/api/causalhypothesis.go (CausalHypothesesView).
export interface CausalHypothesesView {
  generatedAt: string;
  class: string;
  enabled: boolean;
  note: string;
  hypotheses?: CausalHypothesisRow[];
}
export interface CausalHypothesisEvidence {
  kind: string;
  ref: string;
  detail?: string;
}
export interface CausalHypothesisRow {
  id: string;
  subject: string;        // "A ~ B" (direction-free)
  relation: string;       // "co-occurrence" | "observed-adjacency"
  source: string;         // "co-onset" | "audit"
  a?: string;
  b?: string;
  aDirection?: string;    // onset direction (up|down) of A
  bDirection?: string;
  observedFirst?: string; // MEASURED order — NOT a cause
  deltaSeconds?: number;
  coefficient?: number;
  evidence?: CausalHypothesisEvidence[];
  createdAt: string;
}
export interface CausalDirectionResult {
  ok: boolean;
  candidateId: string;
  status?: string;        // "promoted" | "rejected"
  overlayYaml?: string;
  message?: string;
}

// One band-departure. Classed PROJECTED (weakest input = the band). No causal claim.
// Source: obsd/internal/departure/departure.go (Departure).
export interface Departure {
  entityCei: string;
  metric: string;
  at: string;              // RFC3339
  class: string;           // "PROJECTED band ⋈ MEASURED sample (joined, never fused)"
  side: "above" | "below";
  realized: number;        // the MEASURED sample
  lower: number;           // PROJECTED band lower edge
  upper: number;           // PROJECTED band upper edge
  bandWidth: number;
  exceedance: number;      // how far past the band EDGE (>= 0), not past the fire threshold
  confidence: string;      // tight | moderate | wide | "confidence n/a"
  detail: string;          // honest text — no causal/anomaly-score claim
}


// ---------------------------------------------------------------------------
// GET /api/timeline  ->  TimelineView
// Provenance: MIXED, lane-separated (matches/unexplained = MEASURED, projected = PROJECTED).
// Each TimelineSpan carries its own class; lanes are never mixed. Source: obsd/internal/api/timeline.go
// ---------------------------------------------------------------------------
export interface TimelineView {
  generatedAt: string;          // RFC3339
  window: TimelineWindow;
  matches: TimelineSpan[];      // MEASURED phenomenon matches (intervals); always present
  unexplained: TimelineSpan[];  // MEASURED loud-but-unmatched (aging spans); always present
  projected: TimelineSpan[];    // PROJECTED crossing bands; always present (may be [])
  projectedNote: string;        // conditional: OFF (gate) | ON-quiet | ON-with-bands
}

// The [from, to] extent the spans fall within. Source: obsd/internal/api/timeline.go (TimelineWindow).
export interface TimelineWindow {
  from: string;                 // RFC3339
  to: string;                   // RFC3339 (now when empty)
}

// One finding's extent on the time axis. class is the lane's provenance class.
// Source: obsd/internal/api/timeline.go (TimelineSpan).
export interface TimelineSpan {
  class: "MEASURED" | "PROJECTED";
  surface: "insight" | "unexplained" | "early-warning";
  label: string;
  entityCei: string;
  name: string;
  kind: string;
  status: string;               // full|degraded|new|aging|superseded-by-match|resolved (measured)
                                // or the warning's confidence string (projected)
  from: string;                 // RFC3339
  to: string;                   // RFC3339
}


// ---------------------------------------------------------------------------
// GET  /api/context-windows  ->  ContextWindowsView
// POST /api/context-windows  ->  body: ContextWindow (label/kind/startAt/author
//                                required; id/createdAt/spliceEligible server-set),
//                                returns the single stored ContextWindow.
// Provenance: un-classed. An operator ANNOTATION — frames classed data, never fuses.
// Source: obsd/internal/api/context.go
// ---------------------------------------------------------------------------
export interface ContextWindowsView {
  generatedAt: string;          // RFC3339
  windows: ContextWindow[];     // newest start first; always present (may be [])
  spliceCount: number;          // how many are Phase-3 splice points
  note: string;
}

// One operator-defined annotated time span. Un-classed (an annotation, not a datum).
// Source: obsd/internal/api/context.go (ContextWindow).
export interface ContextWindow {
  id: string;                   // server-assigned "cw-N"
  label: string;
  kind: "deploy" | "config-change" | "incident" | "maintenance";
  startAt: string;              // RFC3339
  endAt: string;                // RFC3339; zero ("0001-01-01T00:00:00Z") = open / instantaneous
  annotation: string;
  author: string;
  createdAt: string;            // RFC3339, server-assigned
  spliceEligible: boolean;      // DERIVED: true iff kind is deploy | config-change
}


// ╔══ GROUP 4 — events · incidents · validate-claim · config · chat ══╗

// ===========================================================================
// group: events-incidents-meta  (obsd /api wire contract, branch v3)
// Source of truth: obsd/internal/api/{events,incidents,validate,config,chat}.go
// All views here are camelCase (none are the flow.Chain snake_case exception).
// ===========================================================================


// --- GET /api/events -------------------------------------------------------
// Provenance: MEASURED. The discrete-event lane (v3 T-C): kubelet/control-plane
// Events ingested as MEASURED findings, JOINED (never fused) to gauge phenomena
// on the same workload role. `available:false` => lane off (--events-enabled
// not set); `events` is [] and `note` explains.
// Source: obsd/internal/api/events.go
export interface EventsView {
  class: "MEASURED";
  generatedAt: string;          // time.Time, RFC3339 (UTC)
  graphVersion: string;
  available: boolean;           // false => the events lane is not enabled
  summary: EventsSummary;
  events: EventCard[];          // [] never null
  note: string;
}

// Provenance: MEASURED (headline rollup). Source: obsd/internal/api/events.go
export interface EventsSummary {
  total: number;                // int
  corroborated: number;         // joined to a gauge phenomenon on the same role
  standalone: number;           // visible MEASURED finding, no gauge corroboration this snapshot
  unresolved: number;           // involved object had no resolvable role (instance-keyed)
}

// Provenance: MEASURED (the event itself); corroboration block is a labelled
// AUTHORED-why JOIN, surfaced adjacently, never a cause.
// Source: obsd/internal/api/events.go (EventCard)
export interface EventCard {
  reason: string;
  class: "MEASURED";                  // the event itself
  role: string;                       // the role CEI (or instance key when unresolved)
  roleLabel: string;
  roleUnresolved: boolean;
  entityCei: string;                  // json tag is "entityCei" (lowercase ei)
  namespace: string;
  name: string;
  kind: string;
  count: number;                      // int32
  firstSeen: string;                  // time.Time, RFC3339
  lastSeen: string;                   // time.Time, RFC3339
  lastSeenAgoSeconds: number;         // float64, derived at serve time

  // Corroboration (the JOIN, adjacent — never fused into the event):
  corroborates: string;               // authored gauge phenomenon id ("" = no authored target)
  corroborated: boolean;              // a gauge finding for `corroborates` exists on this role now
  corroborationWhy?: string;          // omitempty: AUTHORED, verbatim (only when corroborated)
  corroborationProvenance?: string;   // omitempty: author@version of the authored why
  summary: string;                    // a MEASURED, register-clean one-liner
}


// --- GET /api/incidents ----------------------------------------------------
// Provenance: MEASURED. Durable cross-run incident memory (v3 T-B): each
// phenomenon-on-a-role joined across time with recurrence count + span.
// recurrence is a deterministic arithmetic consequence of MEASURED findings;
// no field asserts a cause or a projection. `available:false` => memory not
// enabled (needs --incident-memory + a --db store); `incidents` is [].
// Source: obsd/internal/api/incidents.go
export interface IncidentsView {
  class: "MEASURED";
  generatedAt: string;          // time.Time, RFC3339 (UTC)
  graphVersion: string;
  available: boolean;           // false => incident memory not enabled / store absent
  summary: IncidentsSummary;
  incidents: IncidentCard[];    // [] never null
  note: string;
}

// Provenance: MEASURED (headline rollup). Source: obsd/internal/api/incidents.go
export interface IncidentsSummary {
  total: number;                // int
  recurring: number;            // recurrenceCount > 1
  unresolved: number;           // keyed on an instance because the role was unresolvable
}

// Provenance: MEASURED. Source: obsd/internal/api/incidents.go (IncidentCard)
export interface IncidentCard {
  phenomenon: string;
  role: string;                 // the role CEI key (or instance key when unresolved)
  roleLabel: string;
  roleUnresolved: boolean;
  recurrenceCount: number;      // int
  firstSeen: string;            // time.Time, RFC3339
  lastSeen: string;             // time.Time, RFC3339
  lastSeenAgoSeconds: number;   // float64, derived at serve time
  lifespanSeconds: number;      // int64
  summary: string;              // a MEASURED, register-clean one-liner
}


// --- POST /api/validate-claim  (body: { claim: string }) -------------------
// Provenance: un-classed advisory referee (v3 T-D, doc 01 enforcement at the
// surface). It checks an external claim against the charter + authored graph;
// NEVER blocks (the absence of any "blocked" field IS the guarantee).
// When --referee-enabled is unset, server returns {labelledBestEffort:true,
// reasons:[], note:"...not enabled..."} (flagged/matchedAuthored default false).
// Source: obsd/internal/api/validate.go
export interface ValidateClaimRequest {
  claim: string;
}

export interface ClaimVerdict {
  flagged: boolean;
  reasons: ClaimFinding[];      // [] never null
  matchedAuthored: boolean;     // the claim surfaced >=1 real authored relation
  labelledBestEffort: boolean;  // always true
  note: string;
}

// Provenance: un-classed (advisory finding). One thing the referee noticed.
// Source: obsd/internal/api/validate.go (ClaimFinding)
export interface ClaimFinding {
  class:
    | "generated-causation"
    | "class-fusion"
    | "future-certainty"
    | "fusion"
    | "causal"                  // from the denylist (substring) backstop path
    | "advisory";
  detail: string;               // human-readable, quoting the offending text
  advisory: boolean;            // true => not a violation, a phrasing suggestion (does not set flagged)
}


// --- GET /api/config -------------------------------------------------------
// Provenance: un-classed (system configuration, doc 10 M6 — wears no
// MEASURED/PROJECTED/AUTHORED chip). Surfaces runtime posture incl. the
// forecast lane's GATE posture.
// Source: obsd/internal/api/config.go
export interface ConfigView {
  generatedAt: string;          // time.Time, RFC3339 (UTC)
  clusterId: string;
  profile: string;
  paramsVersion: string;
  graphRelease: string;
  graphVersion: string;
  scrapeInterval: string;       // duration string (e.g. "30s")
  evaluationTick: string;       // duration string
  tierBBudget: number;          // int
  forecast: ForecastConfigView;
  note: string;
}

// Provenance: un-classed (the "soon" lane's configuration + gate posture).
// `enabled:false` => lane produces no operator-visible class; `gateNote` states
// why (the gate rule). Source: obsd/internal/api/config.go (ForecastConfigView)
export interface ForecastConfigView {
  enabled: boolean;
  gateNote: string;
  clockdTarget: string;
  interval: string;             // duration string
  horizonSteps: number;         // int
  minContext: number;           // int
  decompose: boolean;
  resetDropFraction: number;    // float64
  maxExplainedFraction: number; // float64
}



// ╔══ GROUP 5 — MCP JSON-RPC client + data-fetch layer + vite proxy ══╗

// ============================================================================
// MCP + DATA-LAYER WIRE CONTRACT  (group: mcp-and-datalayer)
// Source of truth: obsd/internal/mcp/*.go + the api.*View structs returned
// verbatim as tool results + web/src/surfaces/useApi.ts + web/vite.config.ts.
// obsd serves BOTH /api/* and /mcp on the SAME :9095 listener.
// ============================================================================

// ---------------------------------------------------------------------------
// (A) MCP JSON-RPC 2.0 ENVELOPES   route: POST /mcp   (un-classed transport)
//     obsd/internal/mcp/server.go  (rpcReq / rpcResp / rpcErr)
// ---------------------------------------------------------------------------

// JSON-RPC id: json.RawMessage with omitempty. Absent id == a NOTIFICATION
// (no response body -> HTTP 204). Present id is echoed back.
export type McpId = number | string | null;

// Request frame. Provenance: un-classed (transport). route: POST /mcp
export interface McpRequest<P = unknown> {
  jsonrpc: "2.0";
  id?: McpId;          // omit for notifications (e.g. "notifications/initialized")
  method: McpMethod;
  params?: P;          // omitempty
}

export type McpMethod =
  | "initialize"
  | "ping"
  | "tools/list"
  | "tools/call"
  | "notifications/initialized"; // notification: no reply

// Response frame. Provenance: un-classed (transport). route: POST /mcp
// Exactly one of result | error is present.
export interface McpResponse<R = unknown> {
  jsonrpc: "2.0";
  id?: McpId;              // omitempty (mirrors the request id; absent on parse errors)
  result?: R;              // omitempty
  error?: McpError;        // omitempty
}

// JSON-RPC error object. route: POST /mcp
export interface McpError {
  code: McpErrorCode;
  message: string;
}

// rpcErr codes (obsd/internal/mcp/server.go).
export type McpErrorCode =
  | -32700  // parse error          (codeParse)
  | -32600  // invalid request      (codeInvalidRequest — jsonrpc != "2.0")
  | -32601  // method not found     (codeMethodNotFound)
  | -32602; // invalid params       (codeInvalidParams — bad/unknown tool, empty required arg)

// --- initialize ------------------------------------------------------------
// result of method "initialize". route: POST /mcp
export interface McpInitializeResult {
  protocolVersion: string;                 // "2024-11-05"
  capabilities: { tools: Record<string, never> };  // {tools:{}}
  serverInfo: { name: string; version: string };   // {name:"vigil-obsd", version:graphRelease}
  instructions: string;
}

// result of method "ping" is an empty object {}.
export type McpPingResult = Record<string, never>;

// --- tools/list ------------------------------------------------------------
// result of method "tools/list". route: POST /mcp
export interface McpToolsListResult {
  tools: McpToolDef[];
}

// One tool definition (obsd/internal/mcp/server.go toolDef).
export interface McpToolDef {
  name: McpToolName;
  description: string;
  inputSchema: McpInputSchema; // a JSON-Schema object literal
}

// JSON-Schema object the server emits (object/properties/required/additionalProperties).
export interface McpInputSchema {
  type: "object";
  properties: Record<string, { type: string; description?: string }>;
  required?: string[];
  additionalProperties: boolean; // always false
}

// The 7 tool names (obsd/internal/mcp/server.go constants).
export type McpToolName =
  | "get_coverage"        // MEASURED (about own coverage)
  | "get_silence_ledger"  // MEASURED (about own coverage) — the marquee ABSENCE tool
  | "get_warnings"        // PROJECTED
  | "get_incidents"       // MEASURED
  | "get_events"          // MEASURED
  | "validate_claim"      // referee (advisory, never blocks)
  | "emit_advisory";      // ADVISORY (4th class)

// --- tools/call ------------------------------------------------------------
// params of method "tools/call". route: POST /mcp
export interface McpToolCallParams {
  name: McpToolName;
  arguments?: McpToolArguments; // omitempty
}
// Only validate_claim and emit_advisory take arguments.
export type McpToolArguments =
  | { claim: string }   // validate_claim (required, non-empty)
  | { text: string }    // emit_advisory  (the draft prose)
  | Record<string, never>;

// result of method "tools/call" — the MCP content envelope.
// CRITICAL: the typed view lives JSON-stringified inside content[0].text.
// You must JSON.parse(result.content[0].text) a SECOND time. See McpToolText<T>.
export interface McpToolResult {
  content: McpToolContent[];
  isError: boolean;
}
export interface McpToolContent {
  type: "text";
  text: string; // a JSON string — parse it to get McpToolTextOf<name>
}

// Helper: the SHAPE of content[0].text AFTER the second JSON.parse, per tool.
// A disabled lane returns McpLaneOff (NOT a JSON-RPC error; isError stays false).
export type McpToolTextOf =
  | { tool: "get_coverage";       text: CoverageView | McpLaneOff }
  | { tool: "get_silence_ledger"; text: SilenceLedgerView | McpLaneOff }
  | { tool: "get_warnings";       text: WarningsView | McpLaneOff }
  | { tool: "get_incidents";      text: IncidentsView | McpLaneOff }
  | { tool: "get_events";         text: EventsView | McpLaneOff }
  | { tool: "validate_claim";     text: ClaimVerdict | McpLaneOff }
  | { tool: "emit_advisory";      text: AdvisoryResult };

// The honest "lane off / not enabled" payload (obsd/internal/mcp/server.go toolJSON).
// Emitted inside content[0].text when a Source func is nil or the lane is off.
export interface McpLaneOff {
  available: false;
  note: string;
}

// Convenience: double-parse a tools/call response into a typed view.
//   const env  = (await res.json()) as McpResponse<McpToolResult>;   // 1st parse
//   const view = JSON.parse(env.result!.content[0].text) as CoverageView | McpLaneOff; // 2nd
export type McpDoubleParsed<T> = T | McpLaneOff;

// (Section B — most per-tool view types — is intentionally omitted: each tools/call
// result double-parses into the SAME view interface declared above (CoverageView,
// SilenceLedgerView, WarningsView, IncidentsView, EventsView, ClaimVerdict). See
// McpToolTextOf above for the per-tool mapping. The ONE exception, AdvisoryResult,
// has no GET endpoint (emit_advisory is MCP-only), so it is declared here:

// ADVISORY (the 4th class — generated text, never written back). tool emit_advisory.
// obsd/internal/mcp/advisory.go — doubly fenced: REFUSED (banned causal/future-certainty
// register) or WITHHELD (register-clean but mcpAdvisoryGatePassed=false).
export interface AdvisoryResult {
  class: "ADVISORY";       // always "ADVISORY"
  text?: string;           // omitempty — present only if it ever passes the gate
  refused: boolean;
  refusedReason?: string;  // omitempty
  withheld: boolean;
  withheldReason?: string; // omitempty
}

// ---------------------------------------------------------------------------
// (C) DATA-FETCH LAYER CONTRACT  (web/src/surfaces/useApi.ts + vite proxy)
// ---------------------------------------------------------------------------

// The existing GET-poll contract (web/src/surfaces/useApi.ts). Provenance:
// transport. GET-only, default 15s poll aligned to the obsd eval tick, with a
// monotonic-seq guard so an out-of-order slow poll can't overwrite fresher data.
export interface UseApiState<T> {
  data: T | null;
  error: string | null;
  loaded: boolean;
}
// export function useApi<T>(path: string, pollMs?: number): UseApiState<T>;  // pollMs default 15_000

// POST helper contract (NOT covered by useApi, which is GET-only). Needed for
// the three POST routes. No auth header; Content-Type: application/json.
export interface PostJson {
  // POST /api/validate-claim  body {claim}  -> ClaimVerdict
  validateClaim(body: ValidateClaimBody): Promise<ClaimVerdict>;
  // POST /api/context-windows body ContextWindow -> ContextWindow (separate group)
  addContextWindow(body: unknown): Promise<unknown>;
  // POST /mcp                 body McpRequest    -> McpResponse (then double-parse)
  mcp<R = McpToolResult>(req: McpRequest): Promise<McpResponse<R>>;
}
export interface ValidateClaimBody { claim: string; }   // required, non-empty

// Recommended upgrade (per useApi.ts + vite.config.ts + techstack §10):
// replace the 15s poll with an SSE finding-stream or the Connect-Web typed
// client generated from /proto. Until then polling + the seq-guard is the
// contract; the MCP transport itself is a single POST JSON-RPC endpoint today
// (Handle is transport-agnostic, so Streamable-HTTP/stdio can be added later).

// ---------------------------------------------------------------------------
// (D) VITE PROXY ENTRIES needed (web/vite.config.ts server.proxy).
//     /api already exists; /mcp must be ADDED (same :9095 target).
// ---------------------------------------------------------------------------
//   server: {
//     proxy: {
//       "/api": "http://localhost:9095",   // existing
//       "/mcp": "http://localhost:9095",   // ADD — MCP JSON-RPC shares the port
//     },
//   }


// ╔══ GROUP 6 — unexplained (the stated blind spot, doc 08) ══╗

// --- GET /api/unexplained --------------------------------------------------
// Provenance: MEASURED (loud-but-unmatched signal). The STATED blind spot: this
// channel covers KNOWN signals exhibiting UNKNOWN patterns — never a cause, never
// a phenomenon. The system PROPOSES curation candidates; it never authors them.
// Source: obsd/internal/api/unexplained.go (UnexplainedView)
export interface UnexplainedView {
  generatedAt: string;          // RFC3339 (UTC)
  graphVersion: string;
  openCards: UnexplainedCard[] | null;   // loud-but-unmatched, lifecycle-tracked
  candidates: CurationCandidate[] | null; // recurring patterns proposed for human curation
  blindSpot: string;            // the verbatim stated blind-spot notice (always present)
  loudSince?: LoudSinceAnnotation[] | null; // doc 22 C2: onset time per loud (scope, metric)
}

// MEASURED join (doc 22 C2 follow-up): the onset TIME a loud (scope, metric) stepped.
export interface LoudSinceAnnotation {
  scope: string;
  metric: string;
  onsetAt: string;    // RFC3339
  direction: string;  // up | down
  stepZ: number;
}

// MEASURED — one loud-but-unmatched entity, lifecycle-tracked. Source: unexplained.go
export interface UnexplainedCard {
  scope: string;                // the CEI it is loud on
  namespace: string;
  name: string;
  kind: string;                 // "Container" | "Pod" | "Node" | ...
  loudStates: LoudState[];      // which metrics are loud, and how
  matchCheck: string;           // the honest "no phenomenon covered this" note (no causal vocab)
  status: string;               // new | aging | superseded-by-match | resolved
  mark: string;                 // "anomalous — investigate · not-yet-explained"
  firstSeen: string;            // RFC3339
  lastSeen: string;             // RFC3339
  occurrences: number;
  supersededBy: string;         // "" unless a later phenomenon match covered it
  graphVersion: string;
}

// MEASURED — one loud metric state. Source: unexplained.go
export interface LoudState {
  metric: string;
  kind: string;                 // "bar-crossing" | ...
  state: string;                // "above" | "well-above"
  barSource: string;            // "config" | "default"
  flagged: boolean;             // a default-sourced bar (lower trust)
  sampleAt: string;             // RFC3339
}

// MEASURED — a recurring unexplained pattern PROPOSED for human curation
// (never written to the graph). Source: unexplained.go
export interface CurationCandidate {
  metrics: string[];
  entityKind: string;
  windows: number;              // how many windows it has recurred over
  entities: string[];          // the CEIs exhibiting it
  firstSeen: string;            // RFC3339
  lastSeen: string;             // RFC3339
  rationale: string;            // why it is a candidate (verbatim, never a cause)
  graphVersion: string;
}


// ── Governance (doc 20 + doc 12 §3.3): the candidate review queue ─────────────
// Every DGX proposal is status=candidate, firewalled from the deterministic
// detection/forecast path. A NAMED human promotes (authoring a committable overlay)
// or rejects — the system never approves. Source: api/governance.go

// One MEASURED fact a candidate rests on (review without evidence = rejection).
export interface GovernanceEvidence {
  kind: string;                 // e.g. "context:stray-metric", "context:entity", "co-occurrence"
  ref: string;
  detail?: string;
}

// A candidate's DETERMINISTIC support (doc 21 §4): integer COUNTS of agreed MEASURED facts —
// NEVER a model confidence, a weighted sum, or a 0-1 score. Render the counts verbatim and
// rank by the lexicographic tuple; never show a percentage or a synthesized score.
export interface GovernanceSupport {
  evidenceCount: number;
  captureSample: number;
  recurrence: number;
  distinctEntities: number;
  ageSeconds: number;
}

// One candidate as the review surface presents it — full provenance to decide.
export interface GovernanceItem {
  id: string;
  kind: string;                 // node | edge | member | bar_source | causal_hypothesis
  status: string;               // candidate | promoted | rejected | shadow
  subject: string;
  relation?: string;            // associated-with | topology | topo-adjacent | co-occurrence
  source: string;               // cei-fallback | dgx-agent | audit | trace | assoc
  method?: string;
  graphVersion?: string;
  rationale?: string;           // the MODEL's proposed note (PROPOSED context; discarded at promotion)
  evidence: GovernanceEvidence[];
  support: GovernanceSupport;   // deterministic MEASURED support — counts, never a confidence
  // PROJECTED agent enrichment (doc 21 Phase 4 §C): a model-suggested human-readable label +
  // non-causal description for a recurring-anomaly phenomenon candidate. A HINT, discarded at
  // promotion (the human authors the real label); asserts no cause, drives no detection.
  suggestedLabel?: string;
  suggestedDescription?: string;
  suggestedSeverity?: string;   // a SUGGESTED harm level (hint, discarded at promotion)
  suggestedBy?: string;         // the model that produced the hint
  decidedBy?: string;           // the named human (when decided)
  note?: string;                // the human's AUTHORED note
  decidedAt?: string;           // RFC3339
  createdAt: string;
  actionable: boolean;          // false ⇒ a non-actionable k8s object-metadata stray (suppressed from the queue)
}

// GET /api/governance — the pending review queue + the decided audit trail.
export interface GovernanceView {
  class: string;
  available: boolean;           // false ⇒ --dgx-enabled is off
  generatedAt: string;
  graphVersion?: string;
  pending: GovernanceItem[];    // status=candidate AND actionable — the review queue
  decided: GovernanceItem[];    // promoted | rejected | shadow
  counts: Record<string, number>; // by status (all candidates, honest total)
  suppressedMetadata: number;   // status=candidate but NOT enqueued (pure k8s object-metadata strays)
  suppressedNote?: string;      // why those are not actionable
  gateNote: string;             // the harness blocks; a named human approves
  note: string;
}

// POST /api/governance/decide — a named human's call. On promote, carries the
// committable AUTHORED overlay artifact.
export interface GovernanceDecisionResult {
  ok: boolean;
  candidateId: string;
  status?: string;              // promoted | rejected
  overlayYaml?: string;
  message: string;
}

// The deterministic stray→group RESOLUTION delta an equivalence-group promotion would
// produce (doc 21 §4-5) — each list is a set of MEASURED metric names, a count of facts,
// never a confidence. Computed read-only on a scratch graph.
export interface GovernanceEquivPreview {
  groupId: string;
  definesNewGroup: boolean;
  canonical?: string;
  pattern: string;
  newlyResolved: string[];      // strays UNRESOLVED now → RESOLVED after promotion (the coverage move)
  alreadyResolved: string[];    // scope metrics that already resolve (pattern redundant for them)
  stillUnresolved: string[];    // scope metrics the pattern still would not match (honest residue)
}

// GET /api/governance/preview?candidateId=… — a READ-ONLY look at what promoting a
// candidate would author. Never mutates the store or the graph. The overlay's author/note
// are placeholders the named human fills at promotion.
export interface GovernancePreviewResult {
  ok: boolean;
  candidateId: string;
  kind?: string;
  movesCoverage: boolean;       // true ONLY for equiv_group — the one promotion that moves MEASURED coverage
  overlayYaml?: string;
  equiv?: GovernanceEquivPreview;
  caveat?: string;
  message?: string;
}

// GET /api/right-sizing — the off-digest right-sizing ADVISORY (docs/31 §6). Per-workload,
// per-resource recommendations comparing SUSTAINED measured usage (p95 over the window) to the
// workload's OWN declared request/limit. A recommendation a human acts on — never auto-applied.
export interface RightSizingSummary {
  analyzed: number;
  reclaim: number;
  resizeUp: number;
  withinHeadroom: number;
  outOfScope: number;
  unstable: number;
}
export interface RightSizingRow {
  workloadRef: string;
  namespace?: string;
  name?: string;
  workloadKind?: string;
  container?: string;
  resource: "cpu" | "memory" | "storage";
  unit: "millicores" | "bytes";
  qos: string;                  // Guaranteed | Burstable | BestEffort
  p95: number;                  // MEASURED sustained percentile (native unit)
  cv: number;                   // MEASURED coefficient of variation over the window
  samples: number;
  request?: number;             // DECLARED
  limit?: number;               // DECLARED
  action: "reclaim" | "resize-up" | "within-headroom" | "out-of-scope" | "unstable";
  recommended?: number;         // ADVISORY suggestion (request for reclaim, limit for resize-up)
  stable: boolean;
  reason: string;
}
export interface RightSizingView {
  generatedAt: string;
  class: string;
  enabled: boolean;
  windowSeconds: number;        // the EFFECTIVE sustained window the percentile covered (hot-store-limited)
  rules: string;                // the DECLARED advisory thresholds, surfaced so the operator sees the fixed rules
  note: string;
  summary: RightSizingSummary;
  advisories?: RightSizingRow[];
}
