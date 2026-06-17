// Incidents (MEMORY) — /api/incidents. Durable cross-run memory: each phenomenon
// joined across time on its role, with how many distinct episodes (recurrence) it
// has had. MEASURED — a deterministic count and timestamps, never a cause, never a
// forecast. "Has this happened before, and how often."

import { useIncidents } from "@/api/client";
import type { IncidentCard } from "@/api/types";
import { LaneNote, SectionHead, StateDot } from "@/components/ui/primitives";
import { Bar, DataState, Page, Stat4, Tag } from "@/components/ui/widgets";
import { dur, relTime } from "@/lib/format";

function Row({ c, max }: { c: IncidentCard; max: number }) {
  const recurring = c.recurrenceCount > 1;
  return (
    <div className="v-panel p-4">
      <div className="flex flex-wrap items-center gap-2">
        <StateDot sev={recurring ? "degraded" : "neutral"} size={8} />
        <span className="v-mono text-[12px] text-ink-soft">{c.phenomenon}</span>
        {recurring && <Tag sev="degraded">recurring</Tag>}
        {c.roleUnresolved && <Tag sev="info">role unresolved</Tag>}
        <span className="ml-auto font-mono text-[11px] text-ink-mid">×{c.recurrenceCount}</span>
      </div>
      <div className="mt-1 font-mono text-[10.5px] text-ink-low">{c.roleLabel}</div>
      <div className="mt-2.5 flex items-center gap-3">
        <div className="flex-1">
          <Bar
            segments={[
              { value: c.recurrenceCount, sev: recurring ? "degraded" : "info" },
              { value: Math.max(max - c.recurrenceCount, 0), color: "transparent" },
            ]}
            height={6}
          />
        </div>
        <span className="shrink-0 font-mono text-[10px] text-ink-low">
          lifespan {dur(c.lifespanSeconds)} · last {relTime(c.lastSeen)}
        </span>
      </div>
    </div>
  );
}

export function IncidentsPage() {
  const q = useIncidents();
  return (
    <Page>
      <SectionHead
        num="08"
        title="Incidents"
        lede="Durable cross-run recurrence: each phenomenon grouped on its role across time. A deterministic count of distinct episodes — never a cause."
      />
      <DataState q={q}>
        {(d) =>
          !d.available ? (
            <LaneNote kind="off" title="Incident memory is not enabled" note={d.note} />
          ) : (
            <div className="flex flex-col gap-5">
              <Stat4
                items={[
                  { label: "tracked", value: d.summary.total },
                  {
                    label: "recurring",
                    value: d.summary.recurring,
                    sev: d.summary.recurring > 0 ? "degraded" : "neutral",
                  },
                  {
                    label: "unresolved",
                    value: d.summary.unresolved,
                    sev: d.summary.unresolved > 0 ? "info" : "neutral",
                  },
                ]}
              />
              {(d.incidents ?? []).length === 0 ? (
                <LaneNote kind="empty" title="No incidents tracked yet" note={d.note} />
              ) : (
                <div className="flex flex-col gap-2">
                  {[...d.incidents]
                    .sort((a, b) => b.recurrenceCount - a.recurrenceCount)
                    .map((c) => (
                      <Row
                        key={`${c.phenomenon}|${c.role}`}
                        c={c}
                        max={Math.max(...d.incidents.map((x) => x.recurrenceCount))}
                      />
                    ))}
                </div>
              )}
            </div>
          )
        }
      </DataState>
    </Page>
  );
}
