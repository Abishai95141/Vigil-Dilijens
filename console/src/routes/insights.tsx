// Insights (NOW) — /api/insights. The why-feed: MEASURED phenomenon matches with
// their AUTHORED member notes adjacent (the only "why"), cascade stories, and the
// blast radius. A raw-findings toggle shows the unjoined MEASURED rows with the
// fresh-vs-last-seen distinction. Classes are shown side-by-side, never fused.

import { useFindings, useInsights } from "@/api/client";
import type { CascadeCard, InsightCard, MemberRow } from "@/api/types";
import { Icon } from "@/components/ui/icons";
import {
  Button,
  ProvChip,
  SectionHead,
  StateBadge,
  StateDot,
  cx,
} from "@/components/ui/primitives";
import {
  AuthoredNote,
  DataState,
  JoinHint,
  Page,
  Table,
  Tag,
  Td,
  Tr,
} from "@/components/ui/widgets";
import { clock, relTime, shortName } from "@/lib/format";
import type { Severity } from "@/lib/tokens";
import { useState } from "react";

const stateSev = (s: string): Severity =>
  s === "well-above" || s === "well-below"
    ? "firing"
    : s === "above" || s === "below"
      ? "degraded"
      : "neutral";

function Member({ m }: { m: MemberRow }) {
  return (
    <div className="v-panel-inset p-3">
      <div className="flex flex-wrap items-center gap-2">
        <StateDot sev={stateSev(m.state)} size={7} />
        <span className="v-mono text-[12px] text-ink-soft">{m.metric}</span>
        <Tag>{m.role}</Tag>
        <Tag sev={stateSev(m.state)}>{m.state}</Tag>
        <Tag>{m.temporal}</Tag>
        {m.barFlagged && <Tag sev="degraded">default bar</Tag>}
        {m.hop > 0 && <Tag title={m.via}>hop {m.hop}</Tag>}
        {m.edgeResult === "suspect" && <Tag sev="degraded">suspect edge</Tag>}
        <span className="ml-auto font-mono text-[10px] text-ink-low">{clock(m.sampleAt)}</span>
      </div>
      {m.neighbour && (
        <div className="mt-1 font-mono text-[10.5px] text-ink-low">
          via {m.via} → {shortName(m.neighbour.split("|").pop())}
        </div>
      )}
      {m.note && (
        <div className="mt-2">
          <AuthoredNote>{m.note}</AuthoredNote>
        </div>
      )}
    </div>
  );
}

function Card({ c }: { c: InsightCard }) {
  const [open, setOpen] = useState(false);
  const sev: Severity = c.quality === "degraded" ? "info" : "degraded";
  return (
    <div className="v-panel">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full cursor-pointer items-start gap-3 p-4 text-left"
      >
        <Icon.chevron
          size={16}
          className={cx("mt-0.5 shrink-0 text-ink-low transition-transform", open && "rotate-90")}
        />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-[13.5px] font-medium text-ink">{c.label}</span>
            <StateBadge sev={sev}>{c.quality}</StateBadge>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-2 text-[11.5px] text-ink-mid">
            <ProvChip kind="MEASURED" />
            <span className="v-mono text-ink-low">{c.phenomenon}</span>
            <span className="text-ink-low">·</span>
            <span>
              {c.namespace}/{shortName(c.name)}
            </span>
            <Tag>{c.span}</Tag>
            <Tag>
              {c.requiredMet}/{c.requiredTotal} members
            </Tag>
            {(c.blastRadius ?? []).length > 0 && (
              <Tag sev="degraded">{(c.blastRadius ?? []).length} at-risk</Tag>
            )}
          </div>
        </div>
      </button>
      {open && (
        <div className="flex flex-col gap-2 border-t border-rule px-4 py-3">
          <div className="v-eyebrow text-[10px]">
            Evidence trail <JoinHint>MEASURED state · AUTHORED note</JoinHint>
          </div>
          {(c.members ?? []).map((m, i) => (
            <Member key={i} m={m} />
          ))}
          {(c.unobservable ?? []).length > 0 && (
            <div className="text-[11.5px] text-ink-low">
              Unobservable here: <span className="v-mono">{(c.unobservable ?? []).join(", ")}</span>
            </div>
          )}
          {(c.blastRadius ?? []).length > 0 && (
            <div className="mt-1">
              <div className="v-eyebrow mb-1 text-[10px]">
                Blast radius <span className="text-ink-low">— AUTHORED relations, verbatim</span>
              </div>
              <div className="flex flex-col gap-2">
                {(c.blastRadius ?? []).map((b, i) => (
                  <AuthoredNote key={i} source={b.related}>
                    {b.why}
                  </AuthoredNote>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function Cascade({ c }: { c: CascadeCard }) {
  return (
    <div className="v-panel p-4">
      <div className="flex flex-wrap items-center gap-2 text-[12.5px]">
        <StateDot sev="firing" size={8} />
        <span className="v-mono text-ink-soft">{c.trigger.phenomenon}</span>
        <Icon.chevron size={14} className="text-ink-low" />
        <span className="v-mono text-ink-soft">{c.downstream.phenomenon}</span>
        <Tag title={c.related}>{c.related}</Tag>
        <Tag>{c.temporal}</Tag>
      </div>
      <div className="mt-2">
        <AuthoredNote>{c.why}</AuthoredNote>
      </div>
    </div>
  );
}

export function InsightsPage() {
  const q = useInsights();
  const [raw, setRaw] = useState(false);
  const findings = useFindings();

  return (
    <Page>
      <SectionHead
        num="03"
        title="Insights"
        lede="The why-feed: phenomena matched right now, each with its MEASURED evidence and the AUTHORED note that is the only “why” — shown side-by-side, never fused into a generated cause."
        right={
          <Button variant="ghost" active={raw} onClick={() => setRaw((v) => !v)}>
            <Icon.evidence size={14} />
            {raw ? "Joined view" : "Raw findings"}
          </Button>
        }
      />

      {raw ? (
        <DataState q={findings} empty={(d) => (d.findings?.length ?? 0) === 0}>
          {(d) => (
            <Table cols={["phenomenon", "entity", "quality", "members", "last seen"]}>
              {d.findings.map((f, i) => (
                <Tr key={i}>
                  <Td>
                    <span className="text-ink">{f.label}</span>
                    <div className="v-mono text-[10.5px] text-ink-low">{f.phenomenon}</div>
                  </Td>
                  <Td mono>
                    {f.namespace}/{shortName(f.name)}
                  </Td>
                  <Td>
                    <StateBadge sev={f.quality === "degraded" ? "info" : "degraded"}>
                      {f.quality}
                    </StateBadge>
                  </Td>
                  <Td mono>
                    {f.requiredMet}/{f.requiredTotal}
                  </Td>
                  <Td>
                    {f.stale ? (
                      <span className="text-ink-low">{relTime(f.lastSeen)}</span>
                    ) : (
                      <span className="v-live">live</span>
                    )}
                  </Td>
                </Tr>
              ))}
            </Table>
          )}
        </DataState>
      ) : (
        <DataState
          q={q}
          empty={(d) => (d.findings?.length ?? 0) === 0 && (d.cascades?.length ?? 0) === 0}
        >
          {(d) => (
            <div className="flex flex-col gap-5">
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-7">
                {[
                  ["matched", d.summary.total, "neutral"],
                  ["full", d.summary.full, "neutral"],
                  ["degraded", d.summary.degraded, d.summary.degraded > 0 ? "degraded" : "neutral"],
                  ["1-hop", d.summary.firstOrder, "neutral"],
                  ["2-hop", d.summary.secondOrder, "neutral"],
                  ["cascades", d.summary.cascades, d.summary.cascades > 0 ? "firing" : "neutral"],
                  ["at-risk", d.summary.withAtRisk, "neutral"],
                ].map(([label, val, sev]) => (
                  <div key={label as string} className="v-panel-inset px-3 py-2">
                    <div className="v-eyebrow text-[9.5px]">{label}</div>
                    <div
                      className="mt-0.5 font-display text-[20px] font-semibold leading-none"
                      style={{
                        color:
                          sev === "firing"
                            ? "var(--color-error)"
                            : sev === "degraded"
                              ? "var(--color-warning)"
                              : "var(--color-ink)",
                      }}
                    >
                      {val as number}
                    </div>
                  </div>
                ))}
              </div>

              {(d.cascades ?? []).length > 0 && (
                <div>
                  <div className="v-eyebrow mb-2">
                    Cascade stories — co-occurrence ⋈ authored relation
                  </div>
                  <div className="flex flex-col gap-2">
                    {(d.cascades ?? []).map((c, i) => (
                      <Cascade key={i} c={c} />
                    ))}
                  </div>
                </div>
              )}

              <div>
                <div className="v-eyebrow mb-2">Matched phenomena</div>
                <div className="flex flex-col gap-2">
                  {(d.findings ?? []).map((c) => (
                    <Card key={`${c.entityCei}|${c.phenomenon}`} c={c} />
                  ))}
                </div>
              </div>
            </div>
          )}
        </DataState>
      )}
    </Page>
  );
}
