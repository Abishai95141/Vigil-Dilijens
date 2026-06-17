// Coverage (TRUTH) — /api/coverage. What Vigil can and cannot watch on this
// cluster — the charter floor, made visible. Resolvability, the config-bound vs
// default-bar vs unbounded accounting (borrowed normativity: bars come from the
// customer's config, never learned), per-phenomenon observability with verbatim
// missing reasons, and the threshold-rule binding table. MEASURED about own coverage.

import { useCoverage } from "@/api/client";
import type { PhenomenonRow, RuleRow } from "@/api/types";
import { LaneNote, SectionHead } from "@/components/ui/primitives";
import { Bar, DataState, Page, Stat4, Table, Tag, Td, Tr } from "@/components/ui/widgets";
import { pct } from "@/lib/format";
import type { Severity } from "@/lib/tokens";
import { useState } from "react";

const obsSev = (o: string): Severity =>
  o === "full" ? "ok" : o === "partial" ? "degraded" : "neutral";

export function CoveragePage() {
  const q = useCoverage();
  const [obsFilter, setObsFilter] = useState<"all" | "full" | "partial" | "none">("all");

  return (
    <Page>
      <SectionHead
        num="10"
        title="Coverage"
        lede="The honest map of what this cluster lets Vigil watch. Bars come from the customer’s own config — never learned; where nothing is declared, we say so rather than invent a threshold."
      />
      <DataState q={q}>
        {(d) =>
          !d.available ? (
            <LaneNote
              kind="off"
              title="Binding not yet compiled"
              note="Connect a cluster to compile the bound graph."
            />
          ) : (
            <div className="flex flex-col gap-6">
              {/* resolvability + charter floor */}
              <div className="v-panel p-4">
                <div className="flex items-end justify-between gap-4">
                  <div>
                    <div className="v-eyebrow text-[10px]">resolvability</div>
                    <div className="font-display text-[34px] font-semibold leading-none tracking-tight text-ink">
                      {pct(d.summary.resolvability, 1)}
                    </div>
                    <div className="mt-1 text-[11.5px] text-ink-low">
                      {d.summary.entities} entities · {d.summary.tierA} Tier-A
                    </div>
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="mb-1 flex justify-between text-[10.5px] text-ink-low">
                      <span>phenomena observability</span>
                      <span>
                        {d.summary.phenomenaFull} full · {d.summary.phenomenaPartial} partial ·{" "}
                        {d.summary.phenomenaNone} none
                      </span>
                    </div>
                    <Bar
                      segments={[
                        {
                          value: d.summary.phenomenaNone,
                          color: "var(--color-mute)",
                          label: "none",
                        },
                        { value: d.summary.phenomenaPartial, sev: "degraded", label: "partial" },
                        { value: d.summary.phenomenaFull, sev: "ok", label: "full" },
                      ]}
                      height={10}
                    />
                  </div>
                </div>
              </div>

              <Stat4
                items={[
                  {
                    label: "config-bound",
                    value: d.summary.configBound,
                    sub: `of ${d.summary.configEligible} eligible`,
                  },
                  {
                    label: "default bars",
                    value: d.summary.defaultBars,
                    sev: d.summary.defaultBars > 0 ? "degraded" : "neutral",
                    sub: "lower trust",
                  },
                  {
                    label: "QA verified",
                    value: d.summary.qaVerified,
                    sub: `${d.summary.qaSuspect} suspect`,
                  },
                  {
                    label: "QA failed",
                    value: d.summary.qaFailed,
                    sev: d.summary.qaFailed > 0 ? "firing" : "ok",
                  },
                ]}
              />

              {/* per-phenomenon observability */}
              <div>
                <div className="mb-2 flex flex-wrap items-center gap-2">
                  <span className="v-eyebrow">Phenomenon observability</span>
                  <div className="ml-auto flex gap-1">
                    {(["all", "none", "partial", "full"] as const).map((f) => (
                      <button
                        key={f}
                        type="button"
                        onClick={() => setObsFilter(f)}
                        className={`cursor-pointer rounded-[5px] border px-2 py-0.5 font-mono text-[10px] uppercase transition-colors ${
                          obsFilter === f
                            ? "border-ink-low text-ink"
                            : "border-rule-strong text-ink-low hover:text-ink-mid"
                        }`}
                      >
                        {f}
                      </button>
                    ))}
                  </div>
                </div>
                <Table cols={["phenomenon", "observability", "required", "missing reason"]}>
                  {(d.phenomena ?? [])
                    .filter(
                      (p: PhenomenonRow) => obsFilter === "all" || p.observability === obsFilter,
                    )
                    .map((p) => (
                      <Tr key={p.id}>
                        <Td>
                          <span className="text-ink">{p.label}</span>
                          <div className="v-mono text-[10px] text-ink-low">{p.id}</div>
                        </Td>
                        <Td>
                          <Tag sev={obsSev(p.observability)}>{p.observability}</Tag>
                        </Td>
                        <Td mono>
                          {p.requiredObservable}/{p.requiredTotal}
                        </Td>
                        <Td className="text-ink-low">
                          {(p.missingReasons ?? []).join("; ") || "—"}
                        </Td>
                      </Tr>
                    ))}
                </Table>
              </div>

              {/* threshold-rule binding */}
              <div>
                <div className="v-eyebrow mb-2">Threshold-rule binding — the charter floor</div>
                <Table
                  cols={[
                    "rule",
                    "scope",
                    "instantiated",
                    "config",
                    "default",
                    "unbounded",
                    "out-of-scope",
                  ]}
                >
                  {(d.rules ?? []).map((r: RuleRow) => (
                    <Tr key={r.ruleId}>
                      <Td>
                        <span className="v-mono text-[11px] text-ink-soft">{r.ruleId}</span>
                        <div className="text-[10px] text-ink-low">{r.kind}</div>
                      </Td>
                      <Td mono>{r.entityScope}</Td>
                      <Td mono>{r.instantiated}</Td>
                      <Td mono>{r.configBound}</Td>
                      <Td>
                        {r.defaultBound > 0 ? (
                          <Tag sev="degraded">{r.defaultBound}</Tag>
                        ) : (
                          <span className="text-ink-low">0</span>
                        )}
                      </Td>
                      <Td mono>{r.unbounded}</Td>
                      <Td mono>{r.outOfScope}</Td>
                    </Tr>
                  ))}
                </Table>
              </div>

              {(d.caveats ?? []).length > 0 && (
                <div>
                  <div className="v-eyebrow mb-2">Standing honesty notes</div>
                  <div className="v-panel-inset flex flex-col gap-2 p-4 text-[12px] text-ink-mid">
                    {(d.caveats ?? []).map((c, i) => (
                      <div key={i} className="flex gap-2">
                        <span className="text-ink-low">·</span>
                        {c}
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          )
        }
      </DataState>
    </Page>
  );
}
