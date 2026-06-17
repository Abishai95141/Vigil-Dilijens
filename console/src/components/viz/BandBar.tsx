// BandBar — the PROJECTED early-warning band on a time axis. The forecast is an
// interval of WHEN a series crosses its bar: [earliest … latest], never a single
// line. Rendered as a striped (PROJECTED form) band with a dashed point estimate
// inside it; the band's far edge stays OPEN when the crossing may not happen
// within the horizon. Register fixed to "projected to cross" — never "will cross".

import { ProvChip } from "@/components/ui/primitives";
import { clock, relTime } from "@/lib/format";

function ms(iso: string): number {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? 0 : t;
}

export function BandBar({
  basisAt,
  earliestAt,
  crossAt,
  latestAt,
  open,
  barLabel,
  barFlagged,
}: {
  basisAt: string;
  earliestAt: string;
  crossAt: string;
  latestAt: string;
  open: boolean;
  barLabel: string;
  barFlagged?: boolean;
}) {
  const t0 = ms(basisAt);
  const tCross = ms(crossAt);
  const tEarly = ms(earliestAt);
  const tLate = ms(latestAt);
  // domain right edge: the latest edge, or (when open) project past the point estimate.
  const tEnd = open
    ? Math.max(tCross + (tCross - tEarly || 60_000), tLate)
    : Math.max(tLate, tCross);
  const span = Math.max(tEnd - t0, 1);
  const pos = (t: number) => Math.min(100, Math.max(0, ((t - t0) / span) * 100));

  const left = pos(tEarly);
  const right = open ? 100 : pos(tLate);
  const cross = pos(tCross);

  return (
    <div className="select-none">
      <div className="mb-2 flex items-center justify-between text-[11px] text-ink-low">
        <span className="v-mono">basis {clock(basisAt)}</span>
        <span className="v-mono">{open ? "horizon (open)" : clock(latestAt)}</span>
      </div>
      <div className="relative h-12 rounded-[6px] bg-surface-hi">
        {/* the measured "basis" anchor at the left edge */}
        <div className="absolute top-0 bottom-0 left-0 w-px bg-ink-low" />
        {/* the projected band [earliest … latest] — striped, never a flat fill */}
        <div
          className="absolute top-1.5 bottom-1.5 rounded-[4px] border border-dashed border-rule-strong"
          style={{
            left: `${left}%`,
            width: `${Math.max(right - left, 2)}%`,
            background:
              "repeating-linear-gradient(-45deg, transparent, transparent 3px, rgba(107,164,229,.16) 3px, rgba(107,164,229,.16) 6px)",
          }}
        />
        {/* the point estimate (dashed centerline) — shown only WITH the band */}
        <div
          className="absolute top-0 bottom-0"
          style={{
            left: `${cross}%`,
            width: 0,
            borderLeft: "1.5px dashed var(--color-info)",
          }}
        />
        <div
          className="absolute -top-0.5 -translate-x-1/2 rounded-[3px] bg-plane px-1 font-mono text-[9px] text-info"
          style={{ left: `${cross}%` }}
        >
          {clock(crossAt)}
        </div>
        {open && (
          <div className="absolute top-1/2 right-1 -translate-y-1/2 font-mono text-[9px] text-ink-low">
            ↦ may not cross
          </div>
        )}
      </div>
      <div className="mt-2 flex items-center justify-between">
        <span className="inline-flex items-center gap-1.5 text-[11px] text-ink-mid">
          <ProvChip kind="PROJECTED" />
          projected to cross {relTime(crossAt).replace(" ago", "")}
        </span>
        <span
          className="font-mono text-[10.5px]"
          style={{ color: barFlagged ? "var(--color-warning)" : "var(--color-ink-mid)" }}
        >
          bar {barLabel}
          {barFlagged ? " · default" : ""}
        </span>
      </div>
    </div>
  );
}
