import { type TimelineSpan, type TimelineView, entityLabel } from "./types";
import { useApi } from "./useApi";

// The anomaly timeline (doc 10 M4 / §3.3) — findings of all three surfaces laid
// on time, so "what happened, what is happening, what is projected" reads as one
// without class confusion. Matches render as MEASURED intervals; unexplained as
// MEASURED aging spans (dashed); the PROJECTED lane is RESERVED and stated empty
// until the forecasting layer ships (Phase 2) — never a fabricated future.

export function Timeline() {
  const { data, error, loaded } = useApi<TimelineView>("/api/timeline");
  if (!loaded) return <p className="v-muted">Loading timeline…</p>;
  if (error && !data)
    return (
      <div className="v-card" style={{ borderColor: "var(--status-error)" }}>
        <h2>Timeline unavailable</h2>
        <p className="v-muted">
          Could not reach the obsd API ({error}). The timeline needs a findings
          database — start obsd with <code>--db</code>.
        </p>
      </div>
    );
  if (!data) return null;
  const v = data;
  const from = new Date(v.window.from).getTime();
  const to = new Date(v.window.to).getTime();
  const span = Math.max(to - from, 1);
  const pct = (t: string) => {
    const ms = new Date(t).getTime();
    return ((ms - from) / span) * 100;
  };

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-md)",
      }}
    >
      <div className="v-card">
        <div style={{ display: "flex", justifyContent: "space-between" }}>
          <span className="v-overline">
            {new Date(v.window.from).toLocaleString()}
          </span>
          <span className="v-overline">now</span>
        </div>
      </div>

      <Lane
        title="Insights (matched · MEASURED)"
        spans={v.matches}
        pct={pct}
        empty="No matches in the recorded window."
      />
      <Lane
        title="Unexplained (loud · MEASURED)"
        spans={v.unexplained}
        pct={pct}
        empty="No unexplained activity in the recorded window."
      />

      <section>
        <div className="v-overline" style={{ marginBottom: "var(--space-xs)" }}>
          Early warnings (PROJECTED)
        </div>
        <div className="v-tl-projected-empty">{v.projectedNote}</div>
      </section>
    </div>
  );
}

function Lane({
  title,
  spans,
  pct,
  empty,
}: {
  title: string;
  spans: TimelineSpan[];
  pct: (t: string) => number;
  empty: string;
}) {
  return (
    <section>
      <div className="v-overline" style={{ marginBottom: "var(--space-xs)" }}>
        {title} — {spans.length}
      </div>
      {spans.length === 0 ? (
        <p className="v-faint" style={{ fontSize: "var(--text-tiny)" }}>
          {empty}
        </p>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
          {spans.map((s, i) => {
            const left = Math.max(0, Math.min(100, pct(s.from)));
            const right = Math.max(0, Math.min(100, pct(s.to)));
            const width = Math.max(right - left, 0.8);
            return (
              <div
                key={`${s.entityCei}-${s.label}-${i}`}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: "var(--space-sm)",
                }}
              >
                <span
                  className="v-mono v-muted"
                  style={{
                    width: 220,
                    flex: "none",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                    fontSize: "var(--text-tiny)",
                  }}
                  title={`${s.label} · ${entityLabel(s.entityCei)}`}
                >
                  {s.label} · {s.name}
                </span>
                <div className="v-tl-lane" style={{ flex: 1 }}>
                  <div
                    className="v-tl-span"
                    data-surface={s.surface}
                    style={{ left: `${left}%`, width: `${width}%` }}
                    title={`${s.status} · ${new Date(s.from).toLocaleTimeString()} → ${new Date(s.to).toLocaleTimeString()}`}
                  />
                </div>
                <span className="v-badge" data-span="" style={{ flex: "none" }}>
                  {s.status}
                </span>
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
