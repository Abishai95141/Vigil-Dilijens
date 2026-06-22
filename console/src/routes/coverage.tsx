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
import { type Severity, severityColor } from "@/lib/tokens";
import { useState } from "react";

const obsSev = (o: string): Severity =>
  o === "full" ? "ok" : o === "partial" ? "degraded" : "neutral";

// AUTHORED phenomenon severity → a harm-tinted chip class (doc 21 Phase 5). Colour by FORM/hue
// follows the kit's state signals; this is a curated prioritisation label, not a live state.
const sevClass = (s: string): string =>
  s === "critical"
    ? "border-error/50 text-error"
    : s === "high"
      ? "border-warning/50 text-warning"
      : s === "medium"
        ? "border-info/50 text-info"
        : "border-rule text-ink-low";

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
        {(d) => {
          if (!d.available)
            return (
              <LaneNote
                kind="off"
                title="Binding not yet compiled"
                note="Connect a cluster to compile the bound graph."
              />
            );
          // The honest coverage vector — computed from the per-rule rollup so every
          // (entity, variable) pair is accounted in exactly one bucket. Replaces the
          // single 'resolvability' scalar, which only measures bar-declaration among
          // ELIGIBLE config-relative variables (a narrow normativity fill-rate).
          const rules = d.rules ?? [];
          const sum = (k: keyof RuleRow) =>
            rules.reduce((a, r) => a + (typeof r[k] === "number" ? (r[k] as number) : 0), 0);
          const bound = sum("configBound");
          const dflt = sum("defaultBound");
          const unb = sum("unbounded");
          const oos = sum("outOfScope");
          const unr = sum("unresolved");
          const total = bound + dflt + unb + oos + unr;
          const watched = bound + dflt;
          const vec: { n: number; sev?: Severity; color?: string; label: string; hint: string }[] =
            [
              {
                n: bound,
                sev: "ok",
                label: "config-bound",
                hint: "customer declared the bar — highest trust",
              },
              {
                n: dflt,
                sev: "degraded",
                label: "default-bound",
                hint: "fallback bar where none declared — lower trust",
              },
              {
                n: unb,
                color: "var(--color-ink-low)",
                label: "unbounded",
                hint: "watchable, no declared bar — Tier-B-ineligible, listed",
              },
              {
                n: oos,
                color: "var(--color-mute)",
                label: "out-of-scope",
                hint: "the rule does not apply here / no emitting tool",
              },
              ...(unr > 0
                ? [
                    {
                      n: unr,
                      sev: "firing" as Severity,
                      label: "unresolved",
                      hint: "config unreadable at bind time",
                    },
                  ]
                : []),
            ];
          return (
            <div className="flex flex-col gap-6">
              {/* THE COVERAGE VECTOR — the honest composite, never one scalar */}
              <div className="v-panel p-4">
                <div className="mb-3 flex flex-wrap items-end justify-between gap-4">
                  <div>
                    <div className="v-eyebrow text-[10px]">
                      coverage vector — every (entity, variable) pair accounted
                    </div>
                    <div className="mt-1 font-display text-[28px] font-semibold leading-none tracking-tight text-ink">
                      {watched}
                      <span className="text-[16px] text-ink-low"> / {total} watched</span>
                    </div>
                    <div className="mt-1 text-[11.5px] text-ink-low">
                      {d.summary.entities} entities · {d.summary.tierA} Tier-A · 100% accounted
                    </div>
                  </div>
                  <div className="text-right">
                    <div className="v-eyebrow text-[10px]">bars declared where eligible</div>
                    <div className="font-display text-[24px] font-semibold leading-none tracking-tight text-ink">
                      {pct(d.summary.resolvability, 0)}
                    </div>
                    <div className="mt-1 text-[11px] text-ink-low">
                      {bound} declared · {unb} eligible-undeclared
                    </div>
                  </div>
                </div>
                <Bar
                  segments={vec.map((s) => ({
                    value: s.n,
                    sev: s.sev,
                    color: s.color,
                    label: s.label,
                  }))}
                  height={14}
                />
                <div className="mt-3 grid grid-cols-2 gap-x-6 gap-y-1.5 sm:grid-cols-4">
                  {vec.map((s) => (
                    <div key={s.label} className="flex items-start gap-1.5" title={s.hint}>
                      <span
                        className="mt-[3px] inline-block size-2 shrink-0 rounded-full"
                        style={{
                          background:
                            s.color ?? (s.sev ? severityColor(s.sev) : "var(--color-ink-low)"),
                        }}
                      />
                      <div className="min-w-0">
                        <div className="text-[12px] text-ink">
                          <span className="font-mono">{s.n}</span> {s.label}
                        </div>
                        <div className="text-[10.5px] leading-tight text-ink-low">{s.hint}</div>
                      </div>
                    </div>
                  ))}
                </div>
                <div className="mt-3 border-t border-rule pt-2 text-[11px] leading-relaxed text-ink-low">
                  “Resolvability” is the narrow ratio of declared bars among ELIGIBLE
                  config-relative variables — not a measure of how much of the cluster is mapped.
                  Every pair above carries a visible state; the{" "}
                  <span className="text-ink-mid">Silence ledger</span> names each pair’s verbatim
                  reason and the exact config path that would bind it.
                </div>
              </div>

              {/* phenomena observability (signal availability) — its own panel */}
              <div className="v-panel p-4">
                <div className="mb-1.5 flex justify-between text-[10.5px] text-ink-low">
                  <span className="v-eyebrow">phenomena observability — signal availability</span>
                  <span>
                    {d.summary.phenomenaFull} full · {d.summary.phenomenaPartial} partial ·{" "}
                    {d.summary.phenomenaNone} none
                  </span>
                </div>
                <Bar
                  segments={[
                    { value: d.summary.phenomenaNone, color: "var(--color-mute)", label: "none" },
                    { value: d.summary.phenomenaPartial, sev: "degraded", label: "partial" },
                    { value: d.summary.phenomenaFull, sev: "ok", label: "full" },
                  ]}
                  height={10}
                />
                {/* coverage frontier (doc 33 §1): the closeable work, by class */}
                <div className="mt-2 border-t border-[var(--color-line)] pt-2 text-[10.5px] text-ink-low">
                  <span className="v-eyebrow">coverage frontier — what closes it</span>
                  <span className="ml-2 text-ink">
                    {d.summary.frontierCovered} covered · {d.summary.frontierEmission} emission
                    (deploy/scrape/probe) · {d.summary.frontierCompleteness} completeness (author
                    members)
                  </span>
                  <span className="ml-1 text-ink-low">
                    {" "}
                    — attribution (“why”) is the inference agent’s domain, never a coverage gap.
                  </span>
                </div>
              </div>

              <Stat4
                items={[
                  { label: "config-bound", value: bound, sub: `of ${bound + unb} eligible` },
                  {
                    label: "default bars",
                    value: dflt,
                    sev: dflt > 0 ? "degraded" : "neutral",
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
                <Table
                  cols={[
                    "phenomenon",
                    "observability",
                    "required",
                    "missing reason",
                    "how it closes",
                  ]}
                >
                  {(d.phenomena ?? [])
                    .filter(
                      (p: PhenomenonRow) => obsFilter === "all" || p.observability === obsFilter,
                    )
                    .map((p) => (
                      <Tr key={p.id}>
                        <Td>
                          <span className="flex flex-wrap items-center gap-1.5">
                            <span className="text-ink">{p.label}</span>
                            {p.severity && (
                              <span
                                className={`v-mono rounded-[4px] border px-1.5 py-0.5 text-[9.5px] uppercase ${sevClass(p.severity)}`}
                                title="authored harm prioritisation (doc 21 Phase 5)"
                              >
                                {p.severity}
                              </span>
                            )}
                          </span>
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
                        <Td>
                          {p.gapClass && p.gapClass !== "covered" ? (
                            <span className="flex flex-col gap-0.5">
                              <span
                                className={`v-mono w-fit rounded-[4px] border px-1.5 py-0.5 text-[9.5px] uppercase ${
                                  p.gapClass === "emission"
                                    ? "border-[var(--color-warn)] text-[var(--color-warn)]"
                                    : "border-ink-low text-ink-low"
                                }`}
                                title="coverage frontier (doc 33 §1): emission = deploy/scrape/probe; completeness = author members"
                              >
                                {p.gapClass}
                              </span>
                              <span className="text-[11px] text-ink">{p.closer}</span>
                            </span>
                          ) : (
                            <span className="text-ink-low">—</span>
                          )}
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
          );
        }}
      </DataState>
    </Page>
  );
}
