// Anomaly inbox (SOON/NOW) — a unified surface over three DISTINCT notions, each
// kept in its own provenance class, never merged into a single "anomaly score":
//   1. capacity-crossing  (MEASURED)  — a sample crossed a config-sourced bar
//   2. band-departure      (PROJECTED, gate-pending) — a sample left its own band
//   3. unexplained channel (MEASURED, the stated blind spot)
// Ordering is derived from real fields (ladder state, exceedance/bandWidth) —
// never a fabricated numeric severity.

import { useDepartures, useFindings, useUnexplained } from "@/api/client";
import type { Departure, FindingRow, UnexplainedCard } from "@/api/types";
import { Icon } from "@/components/ui/icons";
import { LaneNote, ProvChip, SectionHead, StateDot } from "@/components/ui/primitives";
import { DataState, Page, Tag } from "@/components/ui/widgets";
import { relTime, shortName } from "@/lib/format";
import type { Severity } from "@/lib/tokens";
import type { ReactNode } from "react";

const ladder = (q: string): number => (q === "full" ? 0 : 1);

/* 1 — capacity-crossing (MEASURED) ------------------------------------------ */
function CrossingRow({ f }: { f: FindingRow }) {
  const sev: Severity = f.quality === "degraded" ? "info" : "degraded";
  return (
    <div className="flex items-center gap-3 px-3 py-2.5">
      <StateDot sev={sev} size={8} />
      <div className="min-w-0 flex-1">
        <div className="truncate text-[12.5px] text-ink">{f.label}</div>
        <div className="font-mono text-[10.5px] text-ink-low">
          {f.namespace}/{shortName(f.name)}
        </div>
      </div>
      <Tag>{f.quality}</Tag>
      <ProvChip kind="MEASURED" />
      <span className="w-16 text-right font-mono text-[10px] text-ink-low">
        {f.stale ? relTime(f.lastSeen) : "live"}
      </span>
    </div>
  );
}

/* 2 — band-departure (PROJECTED) — a mini band with the MEASURED point outside */
function DepartureCard({ d }: { d: Departure }) {
  const lo = d.lower;
  const hi = d.upper;
  const pad = Math.max(d.bandWidth * 0.6, Math.abs(d.exceedance) * 1.4, 1);
  const min = Math.min(lo, d.realized) - pad;
  const max = Math.max(hi, d.realized) + pad;
  const span = Math.max(max - min, 1);
  const pos = (v: number) => ((v - min) / span) * 100;
  return (
    <div className="v-panel p-4">
      <div className="flex flex-wrap items-center gap-2">
        <span className="v-mono text-[12px] text-ink-soft">{d.metric}</span>
        <ProvChip kind="PROJECTED" />
        <Tag sev={d.side === "above" ? "firing" : "info"}>{d.side} band</Tag>
        <span className="ml-auto font-mono text-[10.5px] text-ink-low">{d.confidence}</span>
      </div>
      <div className="relative mt-3 h-10 rounded-[6px] bg-surface-hi">
        <div
          className="absolute top-1.5 bottom-1.5 rounded-[4px] border border-dashed border-rule-strong"
          style={{
            left: `${pos(lo)}%`,
            width: `${Math.max(pos(hi) - pos(lo), 2)}%`,
            background:
              "repeating-linear-gradient(-45deg, transparent, transparent 3px, rgba(245,245,244,.06) 3px, rgba(245,245,244,.06) 6px)",
          }}
        />
        <div
          className="absolute top-1/2 size-2.5 -translate-x-1/2 -translate-y-1/2 rounded-full"
          style={{ left: `${pos(d.realized)}%`, background: "var(--color-signal)" }}
          title={`realized ${d.realized}`}
        />
      </div>
      <div className="mt-2 text-[11.5px] text-ink-mid">{d.detail}</div>
    </div>
  );
}

/* 3 — unexplained (MEASURED, blind spot) ------------------------------------ */
function UnexplainedRow({ c }: { c: UnexplainedCard }) {
  return (
    <div className="flex items-center gap-3 px-3 py-2.5">
      <StateDot sev="info" size={8} />
      <div className="min-w-0 flex-1">
        <div className="truncate text-[12.5px] text-ink">{shortName(c.name)}</div>
        <div className="truncate font-mono text-[10.5px] text-ink-low">
          {(c.loudStates ?? []).map((l) => l.metric).join(", ")}
        </div>
      </div>
      <Tag>{c.status}</Tag>
      <Tag>×{c.occurrences}</Tag>
      <ProvChip kind="MEASURED" />
      <span className="w-12 text-right font-mono text-[10px] text-ink-low">
        {relTime(c.lastSeen)}
      </span>
    </div>
  );
}

function Section({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <div>
      <div className="mb-2 flex items-baseline gap-2">
        <span className="v-eyebrow">{title}</span>
        {hint && <span className="text-[11px] text-ink-low">{hint}</span>}
      </div>
      {children}
    </div>
  );
}

export function AnomaliesPage() {
  const findings = useFindings();
  const departures = useDepartures();
  const unexplained = useUnexplained();

  return (
    <Page>
      <SectionHead
        num="07"
        title="Anomaly inbox"
        lede="Three honest notions of “anomalous”, each in its own class and never fused: capacity-crossing (MEASURED), band-departure (PROJECTED), and the unexplained blind spot (MEASURED). No row carries an invented severity score."
      />

      <div className="flex flex-col gap-6">
        {/* 1 — capacity-crossings (MEASURED) */}
        <Section title="Capacity crossings" hint="MEASURED · a sample crossed a config-sourced bar">
          <DataState q={findings} skeletonRows={2} empty={(d) => (d.findings?.length ?? 0) === 0}>
            {(d) => (
              <div className="v-panel divide-y divide-[var(--color-rule)]">
                {[...d.findings]
                  .sort((a, b) => ladder(a.quality) - ladder(b.quality))
                  .map((f, i) => (
                    <CrossingRow key={i} f={f} />
                  ))}
              </div>
            )}
          </DataState>
        </Section>

        {/* 2 — band-departures (PROJECTED, gate-pending) */}
        <Section title="Band departures" hint="PROJECTED · a sample left its own forecast band">
          <DataState q={departures} skeletonRows={1}>
            {(d) =>
              !d.enabled ? (
                <LaneNote kind="off" title="Departure lane is not enabled" note={d.note} />
              ) : !d.active || !d.departures?.length ? (
                <LaneNote
                  kind="pending"
                  title="Computed every tick — withheld until its live gate flips"
                  note={d.note}
                />
              ) : (
                <div className="flex flex-col gap-2">
                  {[...d.departures]
                    .sort((a, b) => b.exceedance / b.bandWidth - a.exceedance / a.bandWidth)
                    .map((x, i) => (
                      <DepartureCard key={i} d={x} />
                    ))}
                </div>
              )
            }
          </DataState>
        </Section>

        {/* 3 — unexplained (MEASURED blind spot) */}
        <Section
          title="Unexplained channel"
          hint="MEASURED · known signals, unknown patterns — investigate, not alarm"
        >
          <DataState q={unexplained} skeletonRows={1}>
            {(d) => (
              <div className="flex flex-col gap-2">
                {(d.openCards?.length ?? 0) > 0 && (
                  <div className="v-panel divide-y divide-[var(--color-rule)]">
                    {d.openCards?.map((c, i) => (
                      <UnexplainedRow key={i} c={c} />
                    ))}
                  </div>
                )}
                {(d.candidates?.length ?? 0) > 0 && (
                  <div className="text-[11.5px] text-ink-low">
                    <Icon.evidence size={12} className="mr-1 inline" />
                    {d.candidates?.length} recurring pattern(s) proposed for human curation — the
                    system proposes, it never authors.
                  </div>
                )}
                <LaneNote kind="info" title="Stated blind spot" note={d.blindSpot} />
              </div>
            )}
          </DataState>
        </Section>
      </div>
    </Page>
  );
}
