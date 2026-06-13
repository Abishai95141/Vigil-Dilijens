import { ProvChip } from "../components/Brand";
import {
  type SilenceRow,
  type WarningCard,
  type WarningsView,
  entityLabel,
  shortMetric,
  shortSpan,
} from "./types";
import { useApi } from "./useApi";

// Plain-English meaning of each guardrail silence code (doc 09 §3.6) — so the
// lane accounting reads as an actionable, grouped statement instead of a flat
// wall of raw enum strings (the audit's surfacing-clarity finding).
const SILENCE_GLOSSARY: Record<string, string> = {
  "no-crossing-within-horizon":
    "rising, but not projected to cross its bar within the horizon",
  "band-too-wide": "the crossing window is too wide to act on (doc 09 §3.6)",
  "flat-series": "flat — no meaningful trend to project",
  "short-context": "not enough history yet to forecast",
  "already-crossed": "already over its bar now — detection's jurisdiction",
  "counter-stream-deferred": "a counter metric; its level is not projectable",
  "clock-degraded": "the forecasting clock is unavailable; warning withheld",
  "decomposition-aborted": "too much of the window was a restart footprint",
  "no-stream": "no resolvable series for this target",
  "ambiguous-stream": "more than one candidate series; not resolvable",
};

// Group silences by reason, most-common first (deterministic by count then name).
function groupByReason(silences: SilenceRow[]): [string, SilenceRow[]][] {
  const m = new Map<string, SilenceRow[]>();
  for (const s of silences) {
    const arr = m.get(s.reason) ?? [];
    arr.push(s);
    m.set(s.reason, arr);
  }
  return [...m.entries()].sort(
    (a, b) => b[1].length - a[1].length || a[0].localeCompare(b[0]),
  );
}

// The Early-warnings surface (doc 10 §3.1 M5 / doc 09 M4) — the "soon" lane.
// PROJECTED-class cards: a projection of a precursor across its bar, ALWAYS
// with its band, plus the graph's "what this precedes" and the blast radius —
// cited to the graph, adjacent, never fused. Register rules are absolute here
// (doc 01 §5): "projected to cross", never "will cross"; bands never collapse
// to a line; an open band edge is stated. When the lane is off (the backtest
// gate, doc 11 §3.5), the surface says WHY instead of implying quiet.

export function EarlyWarnings() {
  const { data, error, loaded } = useApi<WarningsView>("/api/warnings");
  if (!loaded) return <p className="v-muted">Loading early warnings…</p>;
  if (error && !data)
    return (
      <div className="v-card" style={{ borderColor: "var(--status-error)" }}>
        <h2>Early warnings unavailable</h2>
        <p className="v-muted">Could not reach the obsd API ({error}).</p>
      </div>
    );
  if (!data) return null;
  const v = data;

  if (!v.enabled) {
    return (
      <article className="v-card" data-prov="PROJECTED">
        <h3 className="v-overline">Early warnings — off (gate)</h3>
        <p
          className="v-muted"
          style={{
            marginTop: "var(--space-xs)",
            fontSize: "var(--text-small)",
          }}
        >
          {v.gateNote}
        </p>
      </article>
    );
  }

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-lg)",
      }}
    >
      {!v.clock.ready && (
        <article
          className="v-card"
          style={{ borderColor: "var(--rung-at-threshold)" }}
        >
          <h3 className="v-overline">Forecasting degraded</h3>
          <p className="v-muted" style={{ fontSize: "var(--text-small)" }}>
            The clock service is unreachable or not ready
            {v.clock.degradedSince && !v.clock.degradedSince.startsWith("0001")
              ? ` since ${new Date(v.clock.degradedSince).toLocaleTimeString()}`
              : ""}
            . Detection is unaffected; existing warnings age out rather than
            update (doc 14 A13).
          </p>
        </article>
      )}

      <section>
        <h3 className="v-overline" style={{ marginBottom: "var(--space-sm)" }}>
          Projected to cross soon — {v.warnings.length}
        </h3>
        {v.warnings.length === 0 ? (
          <div className="v-card">
            <p className="v-muted">
              No projection crosses a configured bar within the horizon. Silence
              is the default output (doc 09 §3.6) — every quiet target is
              accounted below.
            </p>
          </div>
        ) : (
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              gap: "var(--space-sm)",
            }}
          >
            {v.warnings.map((w) => (
              <Warning key={`${w.entityCei}-${w.metric}`} w={w} />
            ))}
          </div>
        )}
      </section>

      <section className="v-card">
        <div className="v-overline">Lane accounting (honest, per cycle)</div>
        <p className="v-muted" style={{ fontSize: "var(--text-small)" }}>
          {v.silences.length} target(s) silenced by guardrails · {v.unbudgeted}{" "}
          eligible beyond the invocation budget (doc 06 M5)
        </p>
        {v.silences.length > 0 && (
          <div style={{ marginTop: "var(--space-sm)" }}>
            {groupByReason(v.silences).map(([reason, rows]) => (
              <details key={reason} style={{ marginBottom: "var(--space-xs)" }}>
                <summary style={{ cursor: "pointer" }}>
                  <span className="v-badge" data-conf="wide">
                    {rows.length}
                  </span>{" "}
                  <span className="v-mono">{reason}</span>{" "}
                  <span
                    className="v-faint"
                    style={{ fontSize: "var(--text-tiny)" }}
                  >
                    — {SILENCE_GLOSSARY[reason] ?? "silenced by a guardrail"}
                  </span>
                </summary>
                <div style={{ marginTop: 4 }}>
                  {rows.map((s) => (
                    <div key={`${s.entityCei}-${s.metric}`} className="v-evi">
                      <span />
                      <span className="v-mono v-muted">
                        {entityLabel(s.entityCei)} · {shortMetric(s.metric)}
                      </span>
                      <span />
                    </div>
                  ))}
                </div>
              </details>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}

function Warning({ w }: { w: WarningCard }) {
  const cross = new Date(w.crossAt);
  const earliest = new Date(w.earliestAt);
  const fmt = (d: Date) =>
    d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return (
    <article className="v-prov-card" data-prov="PROJECTED">
      <header
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          gap: "var(--space-sm)",
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "var(--space-sm)",
            flexWrap: "wrap",
          }}
        >
          <span aria-hidden style={{ color: "var(--prov-projected-text)" }}>
            ◈
          </span>
          <strong style={{ fontFamily: "var(--font-heading)" }}>
            {entityLabel(w.entityCei)}
          </strong>
          <span className="v-mono" style={{ fontSize: "var(--text-small)" }}>
            {shortMetric(w.metric)}
          </span>
          <span className="v-badge" data-conf={w.confidence}>
            {w.confidence}
          </span>
          {w.aging && (
            <span
              className="v-badge"
              data-conf="wide"
              title="held between refreshes for stability — the last real projection, not re-projected this cycle"
            >
              aging
            </span>
          )}
        </div>
        <ProvChip cls="PROJECTED" />
      </header>

      <p style={{ marginTop: "var(--space-xs)" }}>
        Projected to cross its {w.barSource} bar
        {w.barFlagged ? " (default bar)" : ""} in ~
        {shortSpan(w.timeToCrossSeconds)} (~{fmt(cross)}), band {fmt(earliest)}
        {w.latestBeyondHorizon
          ? " onward — the crossing may not happen within the horizon (band edge open)"
          : `–${fmt(new Date(w.latestAt))}`}
        .
      </p>
      <div className="v-warn-band" aria-hidden>
        <BandStrip w={w} />
      </div>

      {w.precursorPhenomena.length > 0 && (
        <p className="v-authored-note">
          per the graph: known {w.precursorPhenomena.join(", ")} precursor
        </p>
      )}

      {w.atRisk.length > 0 && (
        <div style={{ marginTop: "var(--space-sm)" }}>
          <div className="v-overline">At risk per the graph (AUTHORED)</div>
          {w.atRisk.map((r) => (
            <div key={`${r.ceiKey}-${r.phenomenon}`} className="v-evi">
              <span className="v-mono">{entityLabel(r.ceiKey)}</span>
              <span>
                <span className="v-mono">{r.phenomenon}</span>
                <span className="v-authored-note">
                  per the graph ({r.temporal} · {r.related}): {r.why}
                </span>
              </span>
              <span />
            </div>
          ))}
        </div>
      )}

      <p className="v-faint" style={{ fontSize: "var(--text-tiny)" }}>
        PROJECTED — an extrapolation with a band, never a certainty. Basis{" "}
        {new Date(w.basisAt).toLocaleTimeString()} · {w.contextPoints} context
        points · graph {w.graphVersion.replace("sha256:", "").slice(0, 12)}…
        {w.aging
          ? " · held (aging) — last projection, not refreshed this cycle"
          : ""}
      </p>
    </article>
  );
}

// BandStrip draws the projection as a BAND (never a line, doc 01 §3): the
// horizon as a track, the [earliest, latest] crossing window as a violet
// band, the point estimate as a marker inside it.
function BandStrip({ w }: { w: WarningCard }) {
  const basis = new Date(w.basisAt).getTime();
  const span = Math.max(w.horizonSteps * w.cadenceSeconds * 1000, 1);
  const pct = (t: string) =>
    Math.min(100, Math.max(0, ((new Date(t).getTime() - basis) / span) * 100));
  const left = pct(w.earliestAt);
  const right = w.latestBeyondHorizon ? 100 : pct(w.latestAt);
  const point = pct(w.crossAt);
  return (
    <svg
      width="100%"
      height="14"
      role="img"
      aria-label="projected crossing band"
    >
      <line
        x1="0%"
        y1="7"
        x2="100%"
        y2="7"
        stroke="var(--border-soft)"
        strokeWidth="2"
      />
      <rect
        x={`${left}%`}
        y="2"
        width={`${Math.max(right - left, 1.5)}%`}
        height="10"
        rx="3"
        fill="var(--prov-projected-band)"
        stroke="var(--prov-projected-border)"
      />
      <circle
        cx={`${point}%`}
        cy="7"
        r="4"
        fill="var(--prov-projected-on-dark, var(--prov-projected))"
      />
    </svg>
  );
}
