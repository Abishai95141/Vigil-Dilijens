import type { ConfigView } from "./types";
import { useApi } from "./useApi";

// The Config surface (doc 10 M6). System configuration — cadences, graph release,
// and (the part that matters most) the forecast lane's GATE posture. This is not
// a provenance-classed finding; it wears no MEASURED/PROJECTED/AUTHORED chip. The
// forecast block states honestly whether the "soon" lane is dark and WHY (no
// class is operator-visible before its backtest gate passes, doc 11 §3.5), and it
// shows the decomposition regime (doc 09 §3.4) the forecast context is cleaned by.

function Row({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <div className="v-evi">
      <span className="v-faint">{k}</span>
      <span className={mono ? "v-mono" : undefined}>{v}</span>
      <span />
    </div>
  );
}

export function Config() {
  const { data, error, loaded } = useApi<ConfigView>("/api/config");
  if (!loaded) return <p className="v-muted">Loading configuration…</p>;
  if (error && !data)
    return (
      <div className="v-card" style={{ borderColor: "var(--status-error)" }}>
        <h2>Configuration unavailable</h2>
        <p className="v-muted">Could not reach the obsd API ({error}).</p>
      </div>
    );
  if (!data) return null;
  const c = data;
  const f = c.forecast;
  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-lg)",
      }}
    >
      <header>
        <h2 style={{ fontFamily: "var(--font-heading)" }}>Configuration</h2>
        <p className="v-muted" style={{ fontSize: "var(--text-small)" }}>
          {c.note}
        </p>
      </header>

      <section className="v-card">
        <div className="v-overline">Runtime</div>
        <Row k="cluster" v={c.clusterId} mono />
        <Row k="profile" v={c.profile} />
        <Row k="params version" v={c.paramsVersion} />
        <Row k="graph release" v={c.graphRelease} />
        <Row
          k="graph version"
          v={`${c.graphVersion.replace("sha256:", "").slice(0, 16)}…`}
          mono
        />
        <Row k="scrape interval" v={c.scrapeInterval} />
        <Row k="evaluation tick" v={c.evaluationTick} />
        <Row k="Tier-B budget / cycle" v={String(c.tierBBudget)} />
      </section>

      {/* The forecast-lane card describes the "soon" lane's CONFIGURATION; it is
          not itself a PROJECTED statement, so it carries no provenance class —
          only a violet border to visually associate it with the lane. */}
      <section
        className="v-card"
        style={{
          borderColor: f.enabled
            ? "var(--prov-projected-border)"
            : "var(--rung-at-threshold)",
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "var(--space-sm)",
          }}
        >
          <span className="v-overline">Forecast lane (“soon”)</span>
          <span className="v-badge" data-conf={f.enabled ? "tight" : "wide"}>
            {f.enabled ? "ON" : "OFF — gate"}
          </span>
        </div>
        <p
          className="v-muted"
          style={{
            marginTop: "var(--space-xs)",
            fontSize: "var(--text-small)",
          }}
        >
          {f.gateNote}
        </p>
        <div style={{ marginTop: "var(--space-sm)" }}>
          <Row k="clockd target" v={f.clockdTarget || "—"} mono />
          <Row k="cycle interval" v={f.interval} />
          <Row k="horizon steps" v={String(f.horizonSteps)} />
          <Row k="min context" v={String(f.minContext)} />
        </div>
        <div style={{ marginTop: "var(--space-sm)" }}>
          <div className="v-overline">Decomposition (doc 09 §3.4 / M5)</div>
          <Row k="enabled" v={f.decompose ? "yes" : "no"} />
          <Row k="reset drop fraction" v={f.resetDropFraction.toFixed(2)} />
          <Row
            k="max explained fraction"
            v={f.maxExplainedFraction.toFixed(2)}
          />
          <p className="v-faint" style={{ fontSize: "var(--text-tiny)" }}>
            Splices the forecast context at the last known event boundary (an
            operator deploy/config window, or an auto-detected gauge reset =
            container restart) and projects only the clean remainder — silence
            over forecasting on residue.
          </p>
        </div>
      </section>

      <p className="v-faint" style={{ fontSize: "var(--text-tiny)" }}>
        Detection is non-gating: it runs every {c.evaluationTick} regardless of
        the forecast lane's state (doc 01). The “now” pillar never waits on the
        “soon” pillar.
      </p>
    </div>
  );
}
