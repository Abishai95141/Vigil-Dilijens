import { ProvChip } from "../components/Brand";
import {
  type InsightCard,
  type InsightsView,
  type WarningCard,
  type WarningsView,
  entityLabel,
  shortMetric,
  shortSpan,
} from "./types";
import { useApi } from "./useApi";

// The On-call / mobile surface (doc 10 §3.7 / M8). The SAME findings, the SAME
// class marks, the SAME phrasing rules — delivered on a constrained form factor.
// A warning on a phone still says "projected," still shows its band, still links
// to evidence (doc 10 §3.7); a measured finding is still indicative. Parity of
// marks and registers on the narrow width IS the M8 exit gate, so this surface
// reuses the exact provenance primitives (ProvChip) and the exact phrasing of the
// desktop EarlyWarnings/InsightFeed — it only re-LAYOUTS them, it never re-words.
//
// On-call shows the critical few, not the whole feed: every PROJECTED early
// warning (the "soon", inherently on-call), then the highest-impact MEASURED
// findings (cascade stories + full-quality findings that put something at risk).
// NOTE: criticality/operator-scope ranking (06 M4) is a carry-forward — until it
// lands, "high-impact" is a stated v1 heuristic (has-blast-radius / is-cascade /
// full-quality), not an authored criticality. Stated, never implied.

const MAX_MEASURED = 6;

export function OnCall() {
  const w = useApi<WarningsView>("/api/warnings");
  const i = useApi<InsightsView>("/api/insights");
  const loaded = w.loaded && i.loaded;

  const warnings = w.data?.enabled ? (w.data.warnings ?? []) : [];
  const cascades = i.data?.cascades ?? [];
  // v1 on-call priority among MEASURED findings: at-risk-bearing first, then
  // full quality, then the rest — deterministic (stable by entity for ties).
  const findings = [...(i.data?.findings ?? [])]
    .sort(
      (a, b) => score(b) - score(a) || a.entityCei.localeCompare(b.entityCei),
    )
    .slice(0, MAX_MEASURED);

  return (
    <div style={{ display: "flex", justifyContent: "center" }}>
      <div className="oncall-frame">
        <header
          style={{
            display: "flex",
            alignItems: "baseline",
            justifyContent: "space-between",
            marginBottom: "var(--space-sm)",
          }}
        >
          <h2 style={{ fontFamily: "var(--font-heading)" }}>On-call</h2>
          <span className="v-faint" style={{ fontSize: "var(--text-tiny)" }}>
            triage · same marks &amp; registers
          </span>
        </header>

        {!loaded && <p className="v-muted">Loading…</p>}

        {/* SOON — projected early warnings */}
        {loaded && w.data && !w.data.enabled && (
          <article
            className="v-card"
            data-prov="PROJECTED"
            style={{ marginBottom: "var(--space-sm)" }}
          >
            <div className="v-overline">Early warnings — off (gate)</div>
            <p
              className="v-muted"
              style={{ fontSize: "var(--text-tiny)", marginTop: 4 }}
            >
              {w.data.gateNote}
            </p>
          </article>
        )}
        {warnings.length > 0 && (
          <section style={{ marginBottom: "var(--space-md)" }}>
            <div className="v-overline" style={{ marginBottom: 6 }}>
              Projected to cross soon — {warnings.length}
            </div>
            <div
              style={{
                display: "flex",
                flexDirection: "column",
                gap: "var(--space-sm)",
              }}
            >
              {warnings.map((c) => (
                <MobileWarning key={`${c.entityCei}-${c.metric}`} c={c} />
              ))}
            </div>
          </section>
        )}

        {/* NOW — highest-impact measured findings */}
        <section>
          <div className="v-overline" style={{ marginBottom: 6 }}>
            Matching now {i.data ? `— ${i.data.summary.total}` : ""}
            {i.data && i.data.summary.total > MAX_MEASURED
              ? ` (top ${MAX_MEASURED})`
              : ""}
          </div>
          {loaded && cascades.length === 0 && findings.length === 0 && (
            <p className="v-muted" style={{ fontSize: "var(--text-small)" }}>
              No phenomena matching now.
            </p>
          )}
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              gap: "var(--space-sm)",
            }}
          >
            {cascades.map((cas) => (
              <article
                key={`${cas.trigger.entityCei}-${cas.downstream.phenomenon}`}
                className="v-prov-card"
                data-prov="MEASURED"
              >
                <header
                  style={{
                    display: "flex",
                    justifyContent: "space-between",
                    gap: 8,
                  }}
                >
                  <strong
                    style={{
                      fontFamily: "var(--font-heading)",
                      fontSize: "var(--text-small)",
                    }}
                  >
                    cascade
                  </strong>
                  <ProvChip cls="MEASURED" />
                </header>
                <p style={{ fontSize: "var(--text-small)", marginTop: 4 }}>
                  {cas.trigger.phenomenon} → {cas.downstream.phenomenon}
                </p>
                {/* The relationship is AUTHORED — surfaced verbatim, never fused into a cause. */}
                <p
                  className="v-authored-note"
                  style={{ fontSize: "var(--text-tiny)" }}
                >
                  per the graph ({cas.temporal} · {cas.related}): {cas.why}
                </p>
              </article>
            ))}
            {findings.map((f) => (
              <MobileFinding key={f.entityCei + f.phenomenon} f={f} />
            ))}
          </div>
        </section>
      </div>
    </div>
  );
}

function score(f: InsightCard): number {
  return (f.blastRadius?.length ? 2 : 0) + (f.quality === "full" ? 1 : 0);
}

function MobileWarning({ c }: { c: WarningCard }) {
  const fmt = (s: string) =>
    new Date(s).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return (
    <article className="v-prov-card" data-prov="PROJECTED">
      <header
        style={{ display: "flex", justifyContent: "space-between", gap: 8 }}
      >
        <strong
          style={{
            fontFamily: "var(--font-heading)",
            fontSize: "var(--text-small)",
          }}
        >
          {entityLabel(c.entityCei)}
        </strong>
        <ProvChip cls="PROJECTED" />
      </header>
      {/* Register parity: modal "projected to cross", band ALWAYS shown, open edge stated. */}
      <p style={{ fontSize: "var(--text-small)", marginTop: 4 }}>
        {shortMetric(c.metric)} — projected to cross its {c.barSource} bar
        {c.barFlagged ? " (default)" : ""} in ~{shortSpan(c.timeToCrossSeconds)}
      </p>
      <div className="oncall-band" aria-hidden>
        <MobileBand c={c} />
      </div>
      <p className="v-faint" style={{ fontSize: "var(--text-tiny)" }}>
        band {fmt(c.earliestAt)}
        {c.latestBeyondHorizon ? " onward (open)" : `–${fmt(c.latestAt)}`}
        {c.precursorPhenomena.length > 0
          ? ` · precedes ${c.precursorPhenomena.join(", ")}`
          : ""}
        {c.aging ? " · aging" : ""}
      </p>
    </article>
  );
}

// The minimum useful band rendering on a phone width (doc 10 open question): a
// thin track with the [earliest, latest] window as a violet band, point marker
// inside. Never a line — the band is the promise even at 390px.
function MobileBand({ c }: { c: WarningCard }) {
  const basis = new Date(c.basisAt).getTime();
  const span = Math.max(c.horizonSteps * c.cadenceSeconds * 1000, 1);
  const pct = (t: string) =>
    Math.min(100, Math.max(0, ((new Date(t).getTime() - basis) / span) * 100));
  const left = pct(c.earliestAt);
  const right = c.latestBeyondHorizon ? 100 : pct(c.latestAt);
  const point = pct(c.crossAt);
  return (
    <svg
      width="100%"
      height="10"
      role="img"
      aria-label="projected crossing band"
    >
      <line
        x1="0%"
        y1="5"
        x2="100%"
        y2="5"
        stroke="var(--border-soft)"
        strokeWidth="2"
      />
      <rect
        x={`${left}%`}
        y="1"
        width={`${Math.max(right - left, 2)}%`}
        height="8"
        rx="2"
        fill="var(--prov-projected-band)"
        stroke="var(--prov-projected-border)"
      />
      <circle cx={`${point}%`} cy="5" r="3" fill="var(--prov-projected)" />
    </svg>
  );
}

function MobileFinding({ f }: { f: InsightCard }) {
  return (
    <article className="v-prov-card" data-prov="MEASURED">
      <header
        style={{ display: "flex", justifyContent: "space-between", gap: 8 }}
      >
        <strong
          style={{
            fontFamily: "var(--font-heading)",
            fontSize: "var(--text-small)",
          }}
        >
          {f.label}
        </strong>
        <ProvChip cls="MEASURED" />
      </header>
      {/* Indicative register: a measured state, stated plainly. */}
      <p style={{ fontSize: "var(--text-small)", marginTop: 4 }}>
        {entityLabel(f.entityCei)}
        {f.quality === "degraded" ? " · degraded" : ""}
      </p>
      <p className="v-faint" style={{ fontSize: "var(--text-tiny)" }}>
        {f.span} · {f.requiredMet}/{f.requiredTotal} signals
        {f.blastRadius?.length
          ? ` · ${f.blastRadius.length} at risk per the graph`
          : ""}
      </p>
    </article>
  );
}
