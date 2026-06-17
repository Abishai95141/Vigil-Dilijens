// Events (NOW) — /api/events. Discrete kubelet/control-plane events (OOMKilled,
// CrashLoopBackOff) ingested as MEASURED findings, JOINED (never fused) to a gauge
// phenomenon on the same workload role. A corroborated event carries its AUTHORED
// "why" verbatim; a standalone event is visible but never upgraded to a match.

import { useEvents } from "@/api/client";
import type { EventCard } from "@/api/types";
import { LaneNote, ProvChip, SectionHead, StateDot } from "@/components/ui/primitives";
import { AuthoredNote, DataState, JoinHint, Page, Stat4, Tag } from "@/components/ui/widgets";
import { relTime } from "@/lib/format";

function Card({ e }: { e: EventCard }) {
  return (
    <div className="v-panel p-4">
      <div className="flex flex-wrap items-center gap-2">
        <StateDot sev="firing" size={8} />
        <span className="text-[13.5px] font-medium text-ink">{e.reason}</span>
        <ProvChip kind="MEASURED" />
        {e.corroborated ? <Tag sev="degraded">corroborated</Tag> : <Tag>standalone</Tag>}
        {e.roleUnresolved && <Tag sev="degraded">role unresolved</Tag>}
        <span className="ml-auto font-mono text-[10.5px] text-ink-low">
          ×{e.count} · {relTime(e.lastSeen)}
        </span>
      </div>
      <div className="mt-1.5 text-[12.5px] text-ink-mid">{e.summary}</div>
      <div className="mt-1 font-mono text-[10.5px] text-ink-low">{e.roleLabel}</div>
      {e.corroborated && e.corroborationWhy && (
        <div className="mt-2.5">
          <div className="v-eyebrow mb-1 text-[10px]">
            Corroboration <JoinHint>gauge phenomenon · authored why</JoinHint>
          </div>
          <AuthoredNote source={e.corroborationProvenance}>{e.corroborationWhy}</AuthoredNote>
        </div>
      )}
    </div>
  );
}

export function EventsPage() {
  const q = useEvents();
  return (
    <Page>
      <SectionHead
        num="05"
        title="Events"
        lede="Discrete Kubernetes events as MEASURED findings, joined to a gauge phenomenon by shared role — never fused. A standalone event is real, but never upgraded into a match."
      />
      <DataState q={q}>
        {(d) =>
          !d.available ? (
            <LaneNote kind="off" title="Events lane is not enabled" note={d.note} />
          ) : (
            <div className="flex flex-col gap-5">
              <Stat4
                items={[
                  { label: "total", value: d.summary.total },
                  {
                    label: "corroborated",
                    value: d.summary.corroborated,
                    sev: d.summary.corroborated > 0 ? "degraded" : "neutral",
                  },
                  { label: "standalone", value: d.summary.standalone },
                  {
                    label: "unresolved",
                    value: d.summary.unresolved,
                    sev: d.summary.unresolved > 0 ? "info" : "neutral",
                  },
                ]}
              />
              {(d.events ?? []).length === 0 ? (
                <LaneNote kind="empty" title="No events this snapshot" note={d.note} />
              ) : (
                <div className="flex flex-col gap-2">
                  {(d.events ?? []).map((e, i) => (
                    <Card key={i} e={e} />
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
