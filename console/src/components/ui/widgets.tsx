// Shared page-level building blocks. Every operator surface composes these so
// spacing, typography, and the provenance grammar stay identical across pages.
// Color is reserved for STATE; provenance is carried by FORM (see theme.css).

import { Icon } from "@/components/ui/icons";
import { LaneNote, Skeleton, cx } from "@/components/ui/primitives";
import { type Severity, severityColor } from "@/lib/tokens";
import type { UseQueryResult } from "@tanstack/react-query";
import { type ReactNode, useState } from "react";

/* ── Page — the standard scroll container with a centered measure ─────────── */
export function Page({
  children,
  max = "max-w-6xl",
}: {
  children: ReactNode;
  max?: string;
}) {
  return (
    <div className="h-full overflow-y-auto">
      <div className={cx("mx-auto px-6 py-7", max)}>{children}</div>
    </div>
  );
}

/* ── DataState — one honest loader/error/empty wrapper around a query ──────── */
export function DataState<T>({
  q,
  children,
  empty,
  skeletonRows = 4,
}: {
  q: UseQueryResult<T>;
  children: (data: T) => ReactNode;
  empty?: (data: T) => boolean;
  skeletonRows?: number;
}) {
  if (q.isError) {
    return (
      <LaneNote
        kind="info"
        title="Could not reach obsd"
        note={
          <span>
            The surface failed to load.{" "}
            <span className="v-mono text-ink-low">{String(q.error)}</span>. It will retry on the
            next 15s tick.
          </span>
        }
      />
    );
  }
  if (!q.data) {
    return (
      <div className="flex flex-col gap-2">
        {Array.from({ length: skeletonRows }, (_, i) => (
          <Skeleton key={i} className="h-16 w-full" />
        ))}
      </div>
    );
  }
  if (empty?.(q.data)) {
    return (
      <LaneNote kind="empty" title="Nothing to show right now" note="This surface is quiet." />
    );
  }
  return <>{children(q.data)}</>;
}

/* ── Field / KVGrid — labelled value pairs ───────────────────────────────── */
export function Field({
  label,
  value,
  mono,
}: {
  label: ReactNode;
  value: ReactNode;
  mono?: boolean;
}) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="v-eyebrow text-[10px]">{label}</span>
      <span className={cx("text-[13px] text-ink", mono && "v-mono text-[12px] text-ink-soft")}>
        {value}
      </span>
    </div>
  );
}

export function KVGrid({ children, cols = 3 }: { children: ReactNode; cols?: 2 | 3 | 4 }) {
  const c = cols === 2 ? "sm:grid-cols-2" : cols === 4 ? "sm:grid-cols-4" : "sm:grid-cols-3";
  return <div className={cx("grid grid-cols-1 gap-x-6 gap-y-3", c)}>{children}</div>;
}

/* ── Tag — a small neutral chip (kind/namespace/metric labels) ───────────── */
export function Tag({
  children,
  sev,
  title,
}: {
  children: ReactNode;
  sev?: Severity;
  title?: string;
}) {
  return (
    <span
      title={title}
      className="inline-flex items-center rounded-[4px] border px-1.5 py-[1px] font-mono text-[10px] tracking-[0.04em] text-ink-mid"
      style={
        sev && sev !== "neutral"
          ? { borderColor: `${severityColor(sev)}55`, color: severityColor(sev) }
          : { borderColor: "var(--color-rule-strong)" }
      }
    >
      {children}
    </span>
  );
}

/* ── AuthoredNote — the AUTHORED quote-rule form (verbatim, never paraphrased) */
export function AuthoredNote({
  children,
  source,
  className,
}: {
  children: ReactNode;
  source?: string;
  className?: string;
}) {
  return (
    <div
      className={cx(
        "border-l-2 border-ink-low bg-transparent pl-3 text-[12.5px] italic leading-relaxed text-ink-mid",
        className,
      )}
    >
      <span className="not-italic text-ink-low" aria-hidden>
        “
      </span>
      {children}
      <span className="not-italic text-ink-low" aria-hidden>
        ”
      </span>
      {source && (
        <div className="mt-1 font-mono text-[10px] not-italic tracking-[0.06em] text-ink-low">
          — {source}
        </div>
      )}
    </div>
  );
}

/* ── Stat4 — a compact strip of small KPI tiles bound to real fields ─────── */
export type StatItem = { label: ReactNode; value: ReactNode; sub?: ReactNode; sev?: Severity };
export function Stat4({ items }: { items: StatItem[] }) {
  return (
    <div
      className="grid grid-cols-2 gap-2 sm:grid-cols-4"
      style={{ gridTemplateColumns: `repeat(${Math.min(items.length, 4)}, minmax(0,1fr))` }}
    >
      {items.map((s, i) => (
        <div key={i} className="v-panel-inset px-3 py-2.5">
          <div className="v-eyebrow text-[9.5px] truncate">{s.label}</div>
          <div
            className="mt-0.5 font-display text-[22px] font-semibold leading-none tracking-tight"
            style={{
              color: s.sev && s.sev !== "neutral" ? severityColor(s.sev) : "var(--color-ink)",
            }}
          >
            {s.value}
          </div>
          {s.sub && <div className="mt-1 text-[11px] text-ink-low">{s.sub}</div>}
        </div>
      ))}
    </div>
  );
}

/* ── Bar — a thin segmented/progress bar (watched/silent, full/partial/none) ─ */
export type BarSeg = { value: number; sev?: Severity; color?: string; label?: string };
export function Bar({ segments, height = 8 }: { segments: BarSeg[]; height?: number }) {
  const total = segments.reduce((s, x) => s + x.value, 0) || 1;
  return (
    <div
      className="flex w-full overflow-hidden rounded-full bg-surface-hi"
      style={{ height }}
      role="img"
    >
      {segments.map((s, i) => {
        const c = s.color ?? (s.sev ? severityColor(s.sev) : "var(--color-ink-low)");
        const w = (s.value / total) * 100;
        if (w <= 0) return null;
        return (
          <div
            key={i}
            title={s.label ? `${s.label}: ${s.value}` : undefined}
            style={{ width: `${w}%`, background: c, opacity: 0.85 }}
          />
        );
      })}
    </div>
  );
}

/* ── Table — a compact, hairline-ruled data table ────────────────────────── */
export function Table({
  cols,
  children,
  className,
}: {
  cols: ReactNode[];
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cx("v-panel overflow-hidden", className)}>
      <table className="w-full border-collapse text-[12.5px]">
        <thead>
          <tr className="border-b border-rule-strong">
            {cols.map((c, i) => (
              <th
                key={i}
                className="px-3 py-2 text-left font-mono text-[10px] font-normal tracking-[0.1em] text-ink-low uppercase"
              >
                {c}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}
export function Td({
  children,
  className,
  mono,
  colSpan,
}: {
  children: ReactNode;
  className?: string;
  mono?: boolean;
  colSpan?: number;
}) {
  return (
    <td
      colSpan={colSpan}
      className={cx(
        "px-3 py-2 align-top text-ink-soft",
        mono && "v-mono text-[11.5px] text-ink-mid",
        className,
      )}
    >
      {children}
    </td>
  );
}
export function Tr({ children }: { children: ReactNode }) {
  return <tr className="border-b border-rule last:border-0">{children}</tr>;
}

/* ── CopyButton — copy a string, with a transient "copied" affordance ─────── */
export function CopyButton({ text, label = "Copy" }: { text: string; label?: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      onClick={() => {
        navigator.clipboard?.writeText(text).then(
          () => {
            setDone(true);
            setTimeout(() => setDone(false), 1400);
          },
          () => {},
        );
      }}
      className="inline-flex cursor-pointer items-center gap-1.5 rounded-[5px] border border-rule-strong bg-surface-hi px-2 py-1 text-[11px] text-ink-mid transition-colors hover:text-ink"
    >
      <Icon.evidence size={12} />
      {done ? "Copied" : label}
    </button>
  );
}

/* ── CodeBlock — a copyable mono block (MCP config, examples) ─────────────── */
export function CodeBlock({ code, lang }: { code: string; lang?: string }) {
  return (
    <div className="v-panel-inset relative overflow-hidden">
      <div className="flex items-center justify-between border-b border-rule px-3 py-1.5">
        <span className="v-eyebrow text-[9.5px]">{lang ?? "json"}</span>
        <CopyButton text={code} />
      </div>
      <pre className="overflow-x-auto px-3 py-2.5 text-[11.5px] leading-relaxed text-ink-soft">
        <code>{code}</code>
      </pre>
    </div>
  );
}

/* ── JoinHint — the "⋈ joined, never fused" affordance ───────────────────── */
export function JoinHint({ children }: { children?: ReactNode }) {
  return (
    <span className="inline-flex items-center gap-1.5 font-mono text-[10px] tracking-[0.06em] text-ink-low">
      <span aria-hidden>⋈</span>
      {children ?? "joined, never fused"}
    </span>
  );
}
