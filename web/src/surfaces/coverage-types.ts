// Mirrors obsd/internal/api/coverage.go (CoverageView). Keep field names in sync
// with the Go json tags — drift becomes a visible bug, and the proto contract
// will make it a compile error once the Connect client lands (techstack §10).

export interface CoverageView {
  clusterId: string;
  graphVersion: string;
  graphRelease: string;
  generatedAt: string;
  available: boolean;
  summary: CoverageSummary;
  phenomena: PhenomenonRow[];
  rules: RuleRow[];
  selection: SelectionSummary;
  unbounded: string[];
  caveats: string[];
}

export interface CoverageSummary {
  entities: number;
  tierA: number;
  resolvability: number;
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

export type Observability = "full" | "partial" | "none";

export interface PhenomenonRow {
  id: string;
  label: string;
  observability: Observability;
  requiredTotal: number;
  requiredObservable: number;
  missingReasons: string[];
}

export interface RuleRow {
  ruleId: string;
  kind: string;
  entityScope: string;
  instantiated: number;
  configBound: number;
  defaultBound: number;
  unbounded: number;
  outOfScope: number;
  unresolved: number;
}

export interface SelectionSummary {
  tierA: number;
  noneByReason: Record<string, number>;
}
