import { type Severity, severityColor } from "@/lib/tokens";
import type { CSSProperties, ReactNode } from "react";

function cx(...parts: (string | false | undefined | null)[]): string {
  return parts.filter(Boolean).join(" ");
}

/* ── Panel — the surface tier ────────────────────────────────────────────── */
export function Panel({
  children,
  className,
  inset,
  pad = true,
}: {
  children: ReactNode;
  className?: string;
  inset?: boolean;
  pad?: boolean;
}) {
  return (
    <div className={cx(inset ? "v-panel-inset" : "v-panel", pad && "p-4", className)}>
      {children}
    </div>
  );
}

/* ── Eyebrow — mono section label ────────────────────────────────────────── */
export function Eyebrow({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cx("v-eyebrow", className)}>{children}</div>;
}

/* ── SectionHead — number · title · lede ─────────────────────────────────── */
export function SectionHead({
  num,
  title,
  lede,
  right,
}: {
  num?: string;
  title: ReactNode;
  lede?: ReactNode;
  right?: ReactNode;
}) {
  return (
    <div className="mb-5 flex items-start justify-between gap-6">
      <div className="min-w-0">
        <div className="flex items-baseline gap-3">
          {num && <span className="v-eyebrow">/{num}</span>}
          <h2 className="text-[19px] font-semibold tracking-tight text-ink">{title}</h2>
        </div>
        {lede && (
          <p className="mt-1.5 max-w-[62ch] text-[13px] leading-relaxed text-ink-mid">{lede}</p>
        )}
      </div>
      {right && <div className="shrink-0">{right}</div>}
    </div>
  );
}

/* ── StateDot — the dot+shape severity glyph (shape carries meaning too) ──── */
const dotShape: Record<Severity, string> = {
  ok: "rounded-full",
  info: "rounded-full",
  degraded: "rotate-45 rounded-[2px]", // diamond — degraded
  firing: "rounded-[1px]", // square — firing
  neutral: "rounded-full",
};
export function StateDot({
  sev,
  live,
  size = 8,
}: { sev: Severity; live?: boolean; size?: number }) {
  const c = severityColor(sev);
  return (
    <span
      aria-hidden
      className={cx("inline-block shrink-0", dotShape[sev])}
      style={{
        width: size,
        height: size,
        background: live ? "#fff" : c,
        boxShadow: live ? `0 0 0 2px ${c}` : undefined,
      }}
    />
  );
}

/* ── StateBadge — dot + shape + label, color reserved for state ──────────── */
const sevLabel: Record<Severity, string> = {
  ok: "healthy",
  info: "info",
  degraded: "degraded",
  firing: "firing",
  neutral: "—",
};
const sevTint: Record<Severity, string> = {
  ok: "var(--color-success-tint)",
  info: "var(--color-info-tint)",
  degraded: "var(--color-warning-tint)",
  firing: "var(--color-error-tint)",
  neutral: "transparent",
};
const sevEdge: Record<Severity, string> = {
  ok: "var(--color-success-edge)",
  info: "var(--color-info-edge)",
  degraded: "var(--color-warning-edge)",
  firing: "var(--color-error-edge)",
  neutral: "var(--color-rule-strong)",
};
export function StateBadge({
  sev,
  children,
  uppercase = true,
}: {
  sev: Severity;
  children?: ReactNode;
  uppercase?: boolean;
}) {
  return (
    <span
      className="inline-flex items-center gap-1.5 rounded-[4px] px-2 py-[3px] font-mono text-[10px] tracking-[0.08em]"
      style={{
        background: sevTint[sev],
        border: `1px solid ${sevEdge[sev]}`,
        color: severityColor(sev),
        textTransform: uppercase ? "uppercase" : "none",
      }}
    >
      <StateDot sev={sev} size={7} />
      {children ?? sevLabel[sev]}
    </span>
  );
}

/* ── ProvChip — provenance carried by FORM, not hue ──────────────────────── */
export type ProvKind = "MEASURED" | "PROJECTED" | "AUTHORED";
export function ProvChip({ kind, title }: { kind: ProvKind; title?: string }) {
  const cls =
    kind === "MEASURED"
      ? "v-prov--measured"
      : kind === "PROJECTED"
        ? "v-prov--projected"
        : "v-prov--authored";
  return (
    <span className={cx("v-prov", cls)} title={title ?? kind}>
      {kind}
    </span>
  );
}

/* ── Button — primary / secondary / ghost ────────────────────────────────── */
export function Button({
  children,
  variant = "secondary",
  onClick,
  disabled,
  active,
  className,
  title,
  type = "button",
}: {
  children: ReactNode;
  variant?: "primary" | "secondary" | "ghost";
  onClick?: () => void;
  disabled?: boolean;
  active?: boolean;
  className?: string;
  title?: string;
  type?: "button" | "submit";
}) {
  const base =
    "inline-flex cursor-pointer select-none items-center gap-1.5 rounded-[6px] px-3 py-1.5 text-[12.5px] font-medium transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-40";
  const styles =
    variant === "primary"
      ? "bg-signal text-plane hover:bg-ink"
      : variant === "ghost"
        ? cx("text-ink-mid hover:text-ink", active && "text-ink")
        : cx(
            "border border-rule-strong bg-surface-hi text-ink hover:border-ink-low",
            active && "border-ink-low",
          );
  return (
    <button
      type={type}
      className={cx(base, styles, className)}
      onClick={onClick}
      disabled={disabled}
      title={title}
    >
      {children}
    </button>
  );
}

/* ── Stat — a KPI tile bound to a real field ─────────────────────────────── */
export function Stat({
  label,
  value,
  sub,
  sev,
  hint,
  onClick,
}: {
  label: ReactNode;
  value: ReactNode;
  sub?: ReactNode;
  sev?: Severity;
  hint?: string;
  onClick?: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={hint}
      className={cx(
        "v-panel flex flex-col gap-1 p-4 text-left transition-colors duration-150",
        onClick && "cursor-pointer hover:border-rule-strong",
      )}
      style={{ borderColor: sev && sev !== "neutral" ? `${severityColor(sev)}55` : undefined }}
    >
      <div className="flex items-center gap-2">
        {sev && <StateDot sev={sev} size={7} />}
        <span className="v-eyebrow truncate">{label}</span>
      </div>
      <div
        className="font-display text-[28px] font-semibold leading-none tracking-tight"
        style={{ color: sev && sev !== "neutral" ? severityColor(sev) : "var(--color-ink)" }}
      >
        {value}
      </div>
      {sub && <div className="text-[11.5px] text-ink-low">{sub}</div>}
    </button>
  );
}

/* ── LaneNote — the HONEST off/gate-pending state. Render the note, never empty. */
export function LaneNote({
  kind = "info",
  title,
  note,
}: {
  kind?: "off" | "pending" | "info" | "empty";
  title?: string;
  note?: ReactNode;
}) {
  const label =
    kind === "off"
      ? "lane off"
      : kind === "pending"
        ? "gate-pending"
        : kind === "empty"
          ? "quiet"
          : "note";
  return (
    <div className="v-panel-inset flex items-start gap-3 p-4">
      <span
        className="mt-[3px] font-mono text-[9.5px] tracking-[0.12em] uppercase rounded-[3px] px-1.5 py-0.5"
        style={{ background: "var(--color-surface-hi)", color: "var(--color-ink-low)" }}
      >
        {label}
      </span>
      <div className="min-w-0 text-[12.5px] leading-relaxed text-ink-mid">
        {title && <div className="mb-0.5 font-medium text-ink-soft">{title}</div>}
        {note}
      </div>
    </div>
  );
}

/* ── Spinner / Skeleton ──────────────────────────────────────────────────── */
export function Spinner({ size = 14 }: { size?: number }) {
  return (
    <span
      aria-label="loading"
      className="inline-block animate-spin rounded-full"
      style={{
        width: size,
        height: size,
        border: "1.5px solid var(--color-rule-strong)",
        borderTopColor: "var(--color-ink-mid)",
      }}
    />
  );
}
export function Skeleton({ className, style }: { className?: string; style?: CSSProperties }) {
  return (
    <div
      className={cx("animate-pulse rounded-[5px] bg-surface-hi", className)}
      style={{ animationDuration: "1.6s", ...style }}
    />
  );
}

/* ── Mono — inline monospace value (IDs, metrics, timestamps) ────────────── */
export function Mono({ children, className }: { children: ReactNode; className?: string }) {
  return <span className={cx("v-mono text-[12px] text-ink-soft", className)}>{children}</span>;
}

export { cx };
