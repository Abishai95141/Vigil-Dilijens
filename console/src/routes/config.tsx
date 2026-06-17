// Config (TRUTH) — /api/config. Runtime posture and the forecast lane's GATE.
// Detection runs every evaluation tick regardless of the forecast lane
// (non-gating, doc 01) — the forecast card states plainly why early warnings are
// on or off. Un-classed: system configuration wears no provenance chip.

import { useConfig } from "@/api/client";
import { LaneNote, SectionHead, StateBadge } from "@/components/ui/primitives";
import { AuthoredNote, DataState, Field, KVGrid, Page } from "@/components/ui/widgets";
import { pct } from "@/lib/format";

export function ConfigPage() {
  const q = useConfig();
  return (
    <Page max="max-w-4xl">
      <SectionHead
        num="13"
        title="Config"
        lede="Runtime configuration and the forecast lane’s gate posture. Detection never waits on forecasting — the deterministic path is identical whether the clock is present, degraded, or absent."
      />
      <DataState q={q}>
        {(d) => (
          <div className="flex flex-col gap-5">
            <div className="v-panel p-4">
              <div className="v-eyebrow mb-3">Runtime</div>
              <KVGrid cols={3}>
                <Field label="cluster" value={d.clusterId} mono />
                <Field label="profile" value={d.profile} />
                <Field label="params version" value={d.paramsVersion} mono />
                <Field label="graph release" value={d.graphRelease || "(unreleased dev)"} />
                <Field label="scrape interval" value={d.scrapeInterval} mono />
                <Field label="evaluation tick" value={d.evaluationTick} mono />
                <Field label="Tier-B budget" value={d.tierBBudget} mono />
                <Field
                  label="graph version"
                  value={<span className="break-all">{d.graphVersion.slice(0, 22)}…</span>}
                  mono
                />
              </KVGrid>
            </div>

            <div className="v-panel p-4">
              <div className="mb-3 flex items-center gap-2">
                <span className="v-eyebrow">Forecast lane (“soon”)</span>
                <StateBadge sev={d.forecast.enabled ? "ok" : "neutral"}>
                  {d.forecast.enabled ? "on" : "gated off"}
                </StateBadge>
              </div>
              <AuthoredNote>{d.forecast.gateNote}</AuthoredNote>
              <div className="mt-4">
                <KVGrid cols={3}>
                  <Field label="clockd target" value={d.forecast.clockdTarget} mono />
                  <Field label="interval" value={d.forecast.interval} mono />
                  <Field label="horizon steps" value={d.forecast.horizonSteps} mono />
                  <Field label="min context" value={d.forecast.minContext} mono />
                  <Field label="decompose" value={d.forecast.decompose ? "yes" : "no"} />
                  <Field label="reset drop" value={pct(d.forecast.resetDropFraction)} mono />
                  <Field label="max explained" value={pct(d.forecast.maxExplainedFraction)} mono />
                </KVGrid>
              </div>
            </div>

            <LaneNote kind="info" title="Non-gating" note={d.note} />
          </div>
        )}
      </DataState>
    </Page>
  );
}
