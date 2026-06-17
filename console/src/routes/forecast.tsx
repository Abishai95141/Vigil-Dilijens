// Early warnings (SOON) — /api/warnings. The Tier-B forecast: what is PROJECTED to
// cross a config bar soon. Every card carries a band that never collapses to a
// line; the register is fixed to "projected to cross". The silence list states,
// for every quiet target, the verbatim guardrail reason — "no warning" is a
// statement, not an absence. AUTHORED precursors/blast-radius sit adjacent.

import { useWarnings } from "@/api/client";
import type { WarningCard } from "@/api/types";
import { LaneNote, ProvChip, SectionHead, StateBadge } from "@/components/ui/primitives";
import { AuthoredNote, DataState, Page, Stat4, Tag } from "@/components/ui/widgets";
import { BandBar } from "@/components/viz/BandBar";
import { clock, dur, shortName } from "@/lib/format";

function Card({ w }: { w: WarningCard }) {
  return (
    <div className="v-panel p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-[13.5px] font-medium text-ink">{shortName(w.name)}</span>
            <ProvChip kind="PROJECTED" />
            {w.aging && <Tag>aging</Tag>}
          </div>
          <div className="mt-0.5 font-mono text-[11px] text-ink-low">
            {w.namespace}/{w.kind} · {w.metric} {w.direction} {w.barValue}
            {w.barUnit}
          </div>
        </div>
        <span className="font-mono text-[11px] text-ink-mid">{w.confidence}</span>
      </div>

      <div className="mt-3">
        <BandBar
          basisAt={w.basisAt}
          earliestAt={w.earliestAt}
          crossAt={w.crossAt}
          latestAt={w.latestAt}
          open={w.latestBeyondHorizon}
          barLabel={`${w.barValue}${w.barUnit}`}
          barFlagged={w.barFlagged}
        />
      </div>

      {((w.precursorPhenomena ?? []).length > 0 || (w.atRisk ?? []).length > 0) && (
        <div className="mt-3 border-t border-rule pt-3">
          {(w.precursorPhenomena ?? []).length > 0 && (
            <div className="mb-2 flex flex-wrap items-center gap-1.5">
              <span className="v-eyebrow text-[9.5px]">precursors</span>
              {(w.precursorPhenomena ?? []).map((p) => (
                <Tag key={p}>{p}</Tag>
              ))}
            </div>
          )}
          {(w.atRisk ?? []).map((a, i) => (
            <AuthoredNote key={i} source={a.related}>
              {a.why}
            </AuthoredNote>
          ))}
        </div>
      )}
    </div>
  );
}

export function ForecastPage() {
  const q = useWarnings();
  return (
    <Page>
      <SectionHead
        num="06"
        title="Early warnings"
        lede="The narrow forecasting clock: series PROJECTED to cross a config-declared bar within the horizon. A band of when — never a single line — and never the word “will”."
      />
      <DataState q={q}>
        {(d) =>
          !d.enabled ? (
            <LaneNote kind="off" title="Forecasting lane is gated off" note={d.gateNote} />
          ) : (
            <div className="flex flex-col gap-5">
              <Stat4
                items={[
                  {
                    label: "open warnings",
                    value: d.warnings.length,
                    sev: d.warnings.length > 0 ? "degraded" : "neutral",
                  },
                  {
                    label: "soonest",
                    value: d.warnings.length
                      ? dur(Math.min(...d.warnings.map((w) => w.timeToCrossSeconds)))
                      : "—",
                  },
                  { label: "quiet targets", value: d.silences.length },
                  {
                    label: "clock",
                    value: d.clock.ready ? "ready" : "degraded",
                    sev: d.clock.ready ? "ok" : "degraded",
                    sub: d.clock.ready
                      ? "advisory · never gates"
                      : `since ${clock(d.clock.degradedSince)}`,
                  },
                ]}
              />

              {d.warnings.length === 0 ? (
                <LaneNote
                  kind="empty"
                  title="No projected crossings within the horizon"
                  note="Silence is the default output (doc 09 §3.6) — every eligible series is being watched; none is forecast to cross its bar right now."
                />
              ) : (
                <div className="flex flex-col gap-2">
                  {d.warnings.map((w, i) => (
                    <Card key={i} w={w} />
                  ))}
                </div>
              )}

              {d.silences.length > 0 && (
                <div>
                  <div className="v-eyebrow mb-2">
                    Quiet targets — every silence carries its guardrail reason
                  </div>
                  <div className="v-panel divide-y divide-[var(--color-rule)]">
                    {d.silences.slice(0, 60).map((s, i) => (
                      <div key={i} className="flex items-center gap-3 px-3 py-2 text-[12px]">
                        <span className="v-mono min-w-0 flex-1 truncate text-ink-mid">
                          {shortName(s.entityCei.split("|")[4]) ?? s.entityCei} · {s.metric}
                        </span>
                        <StateBadge sev="neutral" uppercase={false}>
                          {s.reason}
                        </StateBadge>
                      </div>
                    ))}
                  </div>
                  {d.unbudgeted > 0 && (
                    <div className="mt-2 text-[11.5px] text-ink-low">
                      + {d.unbudgeted} eligible target(s) beyond the Tier-B budget this tick.
                    </div>
                  )}
                </div>
              )}
            </div>
          )
        }
      </DataState>
    </Page>
  );
}
