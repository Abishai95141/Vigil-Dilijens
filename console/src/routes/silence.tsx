// Silence ledger (TRUTH) — /api/silence-ledger. The signature surface: every
// (entity,variable) pair the binding produced is either WATCHED or listed here as
// SILENT with a verbatim compiler reason. The completeness invariant —
// totalPairs == watched + silent — is proven on screen. No bar is invented, no
// value learned, no cause asserted. MEASURED about the system's own coverage.

import { useSilenceLedger } from "@/api/client";
import type { SilenceLedgerRow } from "@/api/types";
import { LaneNote, SectionHead } from "@/components/ui/primitives";
import { Bar, DataState, Page, Table, Tag, Td, Tr } from "@/components/ui/widgets";
import { shortName } from "@/lib/format";
import type { Severity } from "@/lib/tokens";
import { useMemo, useState } from "react";

const reasonSev: Record<string, Severity> = {
  unbounded: "neutral",
  "out-of-scope": "neutral",
  "no-stream-key": "degraded",
  unresolved: "firing",
};

const reasonColor: Record<string, string> = {
  unbounded: "var(--color-ink-low)",
  "out-of-scope": "var(--color-mute)",
  "no-stream-key": "var(--color-warning)",
  unresolved: "var(--color-error)",
};

export function SilencePage() {
  const q = useSilenceLedger();
  const [reason, setReason] = useState<string | null>(null);
  const [query, setQuery] = useState("");

  const rows = q.data?.silent ?? [];
  const filtered = useMemo(
    () =>
      rows.filter(
        (r) =>
          (!reason || r.reasonClass === reason) &&
          (!query ||
            r.metric.toLowerCase().includes(query.toLowerCase()) ||
            r.entityCei.toLowerCase().includes(query.toLowerCase()) ||
            r.ruleId.toLowerCase().includes(query.toLowerCase())),
      ),
    [rows, reason, query],
  );

  return (
    <Page>
      <SectionHead
        num="11"
        title="Silence ledger"
        lede="Deterministic absence. Every pair the binding produced is accounted for — watched, or silent with a verbatim reason. “Not watched” is a statement here, never an omission."
      />
      <DataState q={q}>
        {(d) =>
          !d.available ? (
            <LaneNote kind="off" title="Binding not compiled" note={d.note} />
          ) : (
            <div className="flex flex-col gap-5">
              {/* the completeness proof */}
              <div className="v-panel p-4">
                <div className="mb-2 flex flex-wrap items-baseline justify-between gap-2">
                  <span className="v-eyebrow">Completeness — watched + silent = total</span>
                  <span className="font-mono text-[12px] text-ink">
                    {d.summary.watched} + {d.summary.silent} ={" "}
                    <span
                      className="v-live"
                      style={{
                        color:
                          d.summary.watched + d.summary.silent === d.summary.totalPairs
                            ? "var(--color-success)"
                            : "var(--color-error)",
                      }}
                    >
                      {d.summary.totalPairs}
                    </span>
                  </span>
                </div>
                <Bar
                  segments={[
                    { value: d.summary.watched, sev: "ok", label: "watched" },
                    ...Object.entries(d.summary.byReason).map(([k, v]) => ({
                      value: v,
                      color: reasonColor[k] ?? "var(--color-ink-low)",
                      label: k,
                    })),
                  ]}
                  height={12}
                />
                <div className="mt-2 flex flex-wrap gap-3 text-[11px] text-ink-low">
                  <span>
                    <span
                      className="mr-1 inline-block size-2 rounded-full align-middle"
                      style={{ background: "var(--color-success)" }}
                    />
                    watched {d.summary.watched}
                  </span>
                  {Object.entries(d.summary.byReason).map(([k, v]) => (
                    <button
                      key={k}
                      type="button"
                      onClick={() => setReason((r) => (r === k ? null : k))}
                      className={`cursor-pointer transition-colors hover:text-ink ${reason === k ? "text-ink" : ""}`}
                    >
                      <span
                        className="mr-1 inline-block size-2 rounded-full align-middle"
                        style={{ background: reasonColor[k] ?? "var(--color-ink-low)" }}
                      />
                      {k} {v}
                    </button>
                  ))}
                </div>
              </div>

              {/* the filterable ledger */}
              <div>
                <div className="mb-2 flex flex-wrap items-center gap-2">
                  <span className="v-eyebrow">Silent pairs</span>
                  {reason && <Tag sev={reasonSev[reason]}>{reason}</Tag>}
                  <input
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                    placeholder="filter by metric, rule, or entity…"
                    className="ml-auto w-60 rounded-[6px] border border-rule-strong bg-surface-hi px-2.5 py-1 text-[12px] text-ink placeholder:text-ink-low focus:border-ink-low focus:outline-none"
                  />
                  <span className="font-mono text-[10.5px] text-ink-low">{filtered.length}</span>
                </div>
                <Table cols={["entity", "metric", "rule", "state", "reason"]}>
                  {filtered.slice(0, 200).map((r: SilenceLedgerRow, i) => (
                    <Tr key={i}>
                      <Td mono>
                        {shortName(r.entityCei.split("|")[4]) ?? r.entityCei}
                        <div className="text-[10px] text-ink-low">{r.entity}</div>
                      </Td>
                      <Td mono>{r.metric}</Td>
                      <Td mono className="text-ink-low">
                        {r.ruleId}
                      </Td>
                      <Td>
                        <Tag sev={reasonSev[r.reasonClass]}>{r.reasonClass}</Tag>
                      </Td>
                      <Td className="text-ink-low">{r.reason}</Td>
                    </Tr>
                  ))}
                </Table>
                {filtered.length > 200 && (
                  <div className="mt-2 text-[11px] text-ink-low">
                    Showing first 200 of {filtered.length} — narrow the filter to see more.
                  </div>
                )}
              </div>
            </div>
          )
        }
      </DataState>
    </Page>
  );
}
