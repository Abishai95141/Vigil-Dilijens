// Right-sizing (TRUTH) — /api/right-sizing. The off-digest right-sizing ADVISORY (docs/31 §6):
// per-workload, per-resource recommendations comparing a SUSTAINED measured percentile (p95
// over the window) to the workload's OWN declared request/limit. Every recommendation is a
// suggestion a human acts on — the system NEVER auto-applies it. CHARTER + KIT: a recommendation
// is NOT an operational state, so it carries NO state hue (the plane stays monochrome; color is
// reserved for live state elsewhere). Provenance is carried in words/columns: p95 is MEASURED,
// request/limit are DECLARED, the recommendation is advisory (framed "suggest →"). A churny /
// mid-rollout workload yields no recommendation (honest silence), never a noisy number.

import { useRightSizing } from "@/api/client";
import type { RightSizingRow } from "@/api/types";
import { LaneNote, SectionHead } from "@/components/ui/primitives";
import { Bar, DataState, Page, Stat4, Table, Tag, Td, Tr } from "@/components/ui/widgets";
import { bytes, dur, num } from "@/lib/format";
import { useState } from "react";

// A MONOCHROME intensity ladder for the recommendation distribution — actionable verdicts are
// the darkest ink, non-actionable fade to mute. No state hue (the kit reserves color for live
// operational state; an advisory category is not a state — docs/01 + theme.css).
const ACTION_INK: Record<string, string> = {
  reclaim: "var(--color-ink)",
  "resize-up": "var(--color-ink-mid)",
  "within-headroom": "var(--color-ink-low)",
  unstable: "var(--color-mute)",
  "out-of-scope": "var(--color-surface-hi)",
};

// Format a value in its native unit: bytes for memory/storage, millicores for cpu.
const fmtVal = (v: number | undefined, unit: string): string => {
  if (v == null) return "—";
  return unit === "bytes" ? bytes(v) : `${num(Math.round(v))}m`;
};

export function RightSizingPage() {
  const q = useRightSizing();
  const [filter, setFilter] = useState<
    "all" | "reclaim" | "resize-up" | "unstable" | "out-of-scope"
  >("all");

  return (
    <Page>
      <SectionHead
        num="31"
        title="Right-sizing"
        lede="Where a workload's SUSTAINED usage no longer matches what it asked for. The p95 is MEASURED over the window; the request/limit are the customer's OWN declared config; the recommendation is ADVISORY — a human acts on it, the system never does."
      />
      <DataState q={q}>
        {(d) => {
          if (!d.enabled)
            return <LaneNote kind="off" title="Right-sizing advisory lane is off" note={d.note} />;
          const rows = (d.advisories ?? []).filter((r) => filter === "all" || r.action === filter);
          const s = d.summary;
          return (
            <div className="flex flex-col gap-6">
              <Stat4
                items={[
                  {
                    label: "analyzed",
                    value: s.analyzed,
                    sub: `sustained p95 over ${dur(d.windowSeconds)}`,
                  },
                  { label: "reclaim", value: s.reclaim, sub: "over-provisioned request" },
                  { label: "resize-up", value: s.resizeUp, sub: "near/over the limit" },
                  {
                    label: "out of scope",
                    value: s.outOfScope + s.unstable,
                    sub: `${s.unstable} unstable · ${s.outOfScope} no bar/QoS`,
                  },
                ]}
              />

              {/* recommendation distribution — every analyzed pair accounted in one bucket (monochrome) */}
              <div className="v-panel p-4">
                <div className="mb-1.5 flex justify-between text-[10.5px] text-ink-low">
                  <span className="v-eyebrow">
                    recommendation distribution — every analyzed (workload, resource) accounted
                  </span>
                  <span>
                    {s.reclaim} reclaim · {s.resizeUp} resize-up · {s.withinHeadroom}{" "}
                    within-headroom · {s.unstable} unstable · {s.outOfScope} out-of-scope
                  </span>
                </div>
                <Bar
                  segments={[
                    { value: s.reclaim, color: ACTION_INK.reclaim, label: "reclaim" },
                    { value: s.resizeUp, color: ACTION_INK["resize-up"], label: "resize-up" },
                    {
                      value: s.withinHeadroom,
                      color: ACTION_INK["within-headroom"],
                      label: "within-headroom",
                    },
                    { value: s.unstable, color: ACTION_INK.unstable, label: "unstable" },
                    {
                      value: s.outOfScope,
                      color: ACTION_INK["out-of-scope"],
                      label: "out-of-scope",
                    },
                  ]}
                  height={12}
                />
                <div className="mt-3 border-t border-rule pt-2 text-[11px] leading-relaxed text-ink-low">
                  {d.class}. A workload with no stable, declared-resource history in the window
                  simply has no row — the advisory is silent rather than guessing.
                  {d.rules && (
                    <span className="mt-1 block text-ink-low">
                      <span className="text-ink-mid">Rules (fixed in code, never auto-tuned):</span>{" "}
                      {d.rules}
                    </span>
                  )}
                </div>
              </div>

              {/* per-workload advisory */}
              <div>
                <div className="mb-2 flex flex-wrap items-center gap-2">
                  <span className="v-eyebrow">Per-workload advisory</span>
                  <div className="ml-auto flex gap-1">
                    {(["all", "reclaim", "resize-up", "unstable", "out-of-scope"] as const).map(
                      (f) => (
                        <button
                          key={f}
                          type="button"
                          onClick={() => setFilter(f)}
                          className={`cursor-pointer rounded-[5px] border px-2 py-0.5 font-mono text-[10px] uppercase transition-colors ${
                            filter === f
                              ? "border-ink-low text-ink"
                              : "border-rule-strong text-ink-low hover:text-ink-mid"
                          }`}
                        >
                          {f}
                        </button>
                      ),
                    )}
                  </div>
                </div>
                <Table
                  cols={[
                    "workload",
                    "resource",
                    "QoS",
                    "p95 (measured)",
                    "declared",
                    "usage vs bar",
                    "recommendation (advisory)",
                  ]}
                >
                  {rows.map((r: RightSizingRow, i) => {
                    // the bar the usage is judged against: the limit for resize-up, else the request.
                    const barVal = r.action === "resize-up" ? (r.limit ?? 0) : (r.request ?? 0);
                    const frac = barVal > 0 ? Math.min(1, r.p95 / barVal) : 0;
                    const actionable = r.action === "reclaim" || r.action === "resize-up";
                    return (
                      <Tr key={`${r.workloadRef}/${r.container}/${r.resource}/${i}`}>
                        <Td>
                          <span className="text-ink">{r.name || r.workloadRef}</span>
                          {r.container && <span className="text-ink-low"> · {r.container}</span>}
                          <div className="v-mono text-[10px] text-ink-low">
                            {r.workloadKind} · {r.namespace}
                          </div>
                        </Td>
                        <Td>
                          <Tag>{r.resource}</Tag>
                        </Td>
                        <Td>
                          <Tag title="per-resource QoS interpretation">{r.qos}</Tag>
                        </Td>
                        <Td mono>{fmtVal(r.p95, r.unit)}</Td>
                        <Td mono className="text-ink-low">
                          {r.request ? `req ${fmtVal(r.request, r.unit)}` : "req —"}
                          {r.limit ? ` · lim ${fmtVal(r.limit, r.unit)}` : ""}
                        </Td>
                        <Td>
                          {barVal > 0 ? (
                            <div className="w-[110px]">
                              <Bar
                                segments={[
                                  { value: frac, color: "var(--color-ink-mid)" },
                                  { value: 1 - frac, color: "var(--color-surface-hi)" },
                                ]}
                                height={8}
                              />
                              <div className="mt-0.5 v-mono text-[9.5px] text-ink-low">
                                {Math.round(frac * 100)}% of{" "}
                                {r.action === "resize-up" ? "limit" : "request"}
                              </div>
                            </div>
                          ) : (
                            <span className="text-ink-low">—</span>
                          )}
                        </Td>
                        <Td>
                          {/* ADVISORY, not a state: a neutral (monochrome) chip framed "suggest →"; the
                              system proposes, a human decides. Never a state-coloured verdict. */}
                          <span className="flex flex-col gap-0.5">
                            <Tag>
                              {actionable
                                ? `suggest ${r.action}${r.recommended ? ` → ${fmtVal(r.recommended, r.unit)}` : ""}`
                                : r.action}
                            </Tag>
                            <span className="text-[10.5px] leading-tight text-ink-low">
                              {r.reason}
                            </span>
                          </span>
                        </Td>
                      </Tr>
                    );
                  })}
                  {rows.length === 0 && (
                    <Tr>
                      <Td colSpan={7} className="text-ink-low">
                        No advisories in this filter.
                      </Td>
                    </Tr>
                  )}
                </Table>
              </div>

              {(d.advisories ?? []).length === 0 && <LaneNote kind="empty" note={d.note} />}
            </div>
          );
        }}
      </DataState>
    </Page>
  );
}
