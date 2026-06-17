// Timeline (MEMORY) — /api/timeline. Three lanes over a shared time window, never
// mixed: MEASURED match spans, MEASURED unexplained aging spans, and the live
// PROJECTED crossing lane (rendered striped, never as a solid line). When the
// projected lane is quiet, its verbatim note is shown — never a stale placeholder.

import { useTimeline } from "@/api/client";
import type { TimelineSpan } from "@/api/types";
import { LaneNote, ProvChip, SectionHead } from "@/components/ui/primitives";
import { DataState, Page } from "@/components/ui/widgets";
import { clock, shortName } from "@/lib/format";

function ms(iso: string): number {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? 0 : t;
}

function Lane({
  title,
  prov,
  spans,
  from,
  to,
  projected,
  empty,
}: {
  title: string;
  prov: "MEASURED" | "PROJECTED";
  spans: TimelineSpan[];
  from: number;
  to: number;
  projected?: boolean;
  empty?: string;
}) {
  const span = Math.max(to - from, 1);
  const pos = (t: number) => Math.min(100, Math.max(0, ((t - from) / span) * 100));
  return (
    <div className="v-panel-inset p-3">
      <div className="mb-2 flex items-center gap-2">
        <ProvChip kind={prov} />
        <span className="v-eyebrow text-[10px]">{title}</span>
        <span className="ml-auto font-mono text-[10px] text-ink-low">{spans.length}</span>
      </div>
      {spans.length === 0 ? (
        <div className="px-1 py-3 text-[11.5px] text-ink-low">{empty ?? "quiet"}</div>
      ) : (
        <div className="flex flex-col gap-1.5">
          {spans.slice(0, 24).map((s, i) => {
            const a = pos(ms(s.from));
            const b = pos(ms(s.to || s.from));
            return (
              <div key={i} className="flex items-center gap-2">
                <span className="w-40 shrink-0 truncate text-[11px] text-ink-mid" title={s.label}>
                  {shortName(s.name) || s.label}
                </span>
                <div className="relative h-3.5 flex-1 rounded-[3px] bg-surface">
                  <div
                    className="absolute top-0 bottom-0 rounded-[3px]"
                    style={{
                      left: `${a}%`,
                      width: `${Math.max(b - a, 1.5)}%`,
                      background: projected
                        ? "repeating-linear-gradient(-45deg, transparent, transparent 3px, rgba(107,164,229,.28) 3px, rgba(107,164,229,.28) 6px)"
                        : "var(--color-ink-low)",
                      border: projected ? "1px dashed var(--color-info)" : undefined,
                      opacity: 0.9,
                    }}
                    title={`${s.status} · ${clock(s.from)}→${clock(s.to)}`}
                  />
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

export function TimelinePage() {
  const q = useTimeline();
  return (
    <Page>
      <SectionHead
        num="09"
        title="Timeline"
        lede="What was matched, what aged in the unexplained channel, and what is projected to cross — three lanes over one window, each in its own class, never mixed."
      />
      <DataState q={q}>
        {(d) => {
          const from = ms(d.window.from);
          const to = ms(d.window.to);
          return (
            <div className="flex flex-col gap-3">
              <div className="flex items-center justify-between font-mono text-[10.5px] text-ink-low">
                <span>{clock(d.window.from)}</span>
                <span>{clock(d.window.to)}</span>
              </div>
              <Lane
                title="Matches"
                prov="MEASURED"
                spans={d.matches ?? []}
                from={from}
                to={to}
                empty="no matches in window"
              />
              <Lane
                title="Unexplained (aging)"
                prov="MEASURED"
                spans={d.unexplained ?? []}
                from={from}
                to={to}
                empty="no aging unexplained spans"
              />
              <Lane
                title="Projected crossings"
                prov="PROJECTED"
                spans={d.projected ?? []}
                from={from}
                to={to}
                projected
                empty={d.projectedNote}
              />
              {(d.projected ?? []).length === 0 && (
                <LaneNote kind="info" title="Projected lane" note={d.projectedNote} />
              )}
            </div>
          );
        }}
      </DataState>
    </Page>
  );
}
