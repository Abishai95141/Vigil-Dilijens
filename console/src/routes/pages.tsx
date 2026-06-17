import {
  useCoverage,
  useEvents,
  useIncidents,
  useInsights,
  useSilenceLedger,
  useWarnings,
} from "@/api/client";
import { ClusterGraph } from "@/components/graph/ClusterGraph";
import { Icon } from "@/components/ui/icons";
import { LaneNote, Panel, SectionHead, Stat } from "@/components/ui/primitives";
import { pct } from "@/lib/format";
import type { ReactNode } from "react";

/** Full-bleed scroll container for content pages. */
function Page({ children, max = "max-w-6xl" }: { children: ReactNode; max?: string }) {
  return (
    <div className="h-full overflow-y-auto">
      <div className={`mx-auto ${max} px-6 py-7`}>{children}</div>
    </div>
  );
}

/* ── Cluster graph — the headline (full bleed) ───────────────────────────── */
export function GraphPage() {
  return <ClusterGraph />;
}

/* ── Overview — the command center (live KPIs) ───────────────────────────── */
export function OverviewPage() {
  const ins = useInsights().data;
  const warn = useWarnings().data;
  const cov = useCoverage().data;
  const sil = useSilenceLedger().data;
  const inc = useIncidents().data;
  const ev = useEvents().data;

  const cascades = ins?.summary?.cascades ?? 0;
  const accounted =
    sil?.summary && sil.summary.totalPairs === sil.summary.watched + sil.summary.silent;

  return (
    <Page>
      <SectionHead
        num="01"
        title="Overview"
        lede="What is happening now, what crosses a bar soon, and what we are honestly not watching. Color is reserved for state; every figure links to its surface."
      />
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Stat
          label="Active findings"
          value={ins?.summary?.total ?? "—"}
          sub={`${ins?.summary?.degraded ?? 0} degraded · ${ins?.summary?.full ?? 0} full`}
          sev={(ins?.summary?.degraded ?? 0) > 0 ? "degraded" : "ok"}
        />
        <Stat
          label="Cascades active"
          value={cascades}
          sub={cascades > 0 ? "a chain reaction is firing" : "none firing"}
          sev={cascades > 0 ? "firing" : "ok"}
        />
        <Stat
          label="Forecast warnings"
          value={warn?.enabled ? (warn?.warnings?.length ?? 0) : "gated"}
          sub={warn?.enabled ? "projected to cross soon" : "lane behind its gate"}
          sev={warn?.enabled && (warn?.warnings?.length ?? 0) > 0 ? "degraded" : "neutral"}
        />
        <Stat
          label="Recurring incidents"
          value={inc?.summary?.recurring ?? "—"}
          sub={`${inc?.summary?.total ?? 0} tracked across runs`}
          sev={(inc?.summary?.recurring ?? 0) > 0 ? "degraded" : "ok"}
        />
        <Stat
          label="Observability"
          value={pct(cov?.summary?.resolvability)}
          sub={`${cov?.summary?.phenomenaFull ?? 0} full · ${cov?.summary?.phenomenaNone ?? 0} none`}
          sev="info"
        />
        <Stat
          label="Silence accounting"
          value={accounted ? "100%" : "check"}
          sub={`${sil?.summary?.watched ?? 0} watched · ${sil?.summary?.silent ?? 0} silent`}
          sev={accounted ? "ok" : "degraded"}
        />
        <Stat
          label="Events"
          value={ev?.summary?.total ?? "—"}
          sub={`${ev?.summary?.standalone ?? 0} standalone`}
          sev="neutral"
        />
        <Stat label="Unexplained" value="—" sub="the stated blind spot" sev="info" />
      </div>

      <div className="mt-7">
        <SectionHead num="02" title="The chain reaction" />
        <Panel>
          <div className="flex items-center gap-2 text-[13px] text-ink-mid">
            <Icon.graph size={16} />
            Open the <span className="text-ink">Cluster graph</span> to trace dependencies, isolate
            failures, and follow a cascade end-to-end.
          </div>
        </Panel>
      </div>
    </Page>
  );
}

/* ── Honest stub for surfaces under construction ─────────────────────────── */
export function Stub({
  num,
  title,
  lede,
  note,
}: {
  num: string;
  title: string;
  lede: string;
  note: string;
}) {
  return (
    <Page>
      <SectionHead num={num} title={title} lede={lede} />
      <LaneNote kind="info" title="Surface under construction" note={note} />
    </Page>
  );
}
