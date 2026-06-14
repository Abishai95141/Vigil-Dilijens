import { ProvChip } from "../components/Brand";
import type { CrossServiceLink, CrossServiceView } from "./types";
import { useApi } from "./useApi";

// The Cross-service cascade surface (doc 15 phase F). The warm-path chain that
// names the most-upstream degraded WORKLOAD and its impacted callers over
// OBSERVED-FLOW edges, with the ONE authored relation surfaced verbatim. It is
// the JOIN, never the fusion: the MEASURED degradation, the MEASURED observed-
// flow edge, the AUTHORED "why", and the structural position sit side by side,
// each wearing its label (doc 01 §5). Three honest lane states: OFF (flow
// discovery not running), quiet (no degraded callee has an impacted caller this
// tick), and active (a chain is firing) — distinct, never conflated.

export function CrossService() {
  const { data, error, loaded } =
    useApi<CrossServiceView>("/api/cross-service");
  if (!loaded) return <p className="v-muted">Loading cross-service…</p>;
  if (error && !data)
    return (
      <div className="v-card" style={{ borderColor: "var(--status-error)" }}>
        <h2>Cross-service unavailable</h2>
        <p className="v-muted">Could not reach the obsd API ({error}).</p>
      </div>
    );
  if (!data) return null;
  const v = data;

  // OFF / quiet: state WHY there is no chain (the gate-rule honesty), never
  // imply the absence of cross-service dependency.
  if (!v.enabled || !v.active || !v.chain) {
    return (
      <article className="v-card" data-prov="MEASURED">
        <h3 className="v-overline">
          Cross-service cascade — {v.enabled ? "quiet" : "off"}
        </h3>
        <p
          style={{
            marginTop: "var(--space-xs)",
            fontSize: "var(--text-small)",
          }}
        >
          {v.note}
        </p>
        <p
          className="v-faint"
          style={{ marginTop: "var(--space-sm)", fontSize: "var(--text-tiny)" }}
        >
          {v.gateNote}
        </p>
      </article>
    );
  }

  const c = v.chain;
  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-lg)",
      }}
    >
      <article className="v-prov-card" data-prov="MEASURED">
        <header
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            gap: "var(--space-sm)",
            flexWrap: "wrap",
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
            <span className="v-overline">Most-upstream degraded node</span>
            <strong
              className="v-mono"
              style={{ fontFamily: "var(--font-heading)" }}
            >
              {c.most_upstream_degraded_node}
            </strong>
          </div>
          <ProvChip cls="MEASURED" />
        </header>
        <p
          className="v-faint"
          style={{ marginTop: "var(--space-xs)", fontSize: "var(--text-tiny)" }}
        >
          {c.node_class} — {c.node_basis}
        </p>
      </article>

      <section>
        <h3 className="v-overline" style={{ marginBottom: "var(--space-sm)" }}>
          Impacted callers over observed-flow edges — {c.chain.length}
        </h3>
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "var(--space-sm)",
          }}
        >
          {c.chain.map((l) => (
            <Link key={`${l.impacted}-${l.degraded}`} l={l} />
          ))}
        </div>
      </section>

      {c.symptoms.length > 0 && (
        <section className="v-card">
          <div className="v-overline">Symptoms fed to the walk (MEASURED)</div>
          <div style={{ marginTop: "var(--space-sm)" }}>
            {c.symptoms.map((s) => (
              <div key={`${s.workload}-${s.phenomenon}`} className="v-evi">
                <span className="v-mono">{s.workload}</span>
                <span>
                  <span className="v-mono">{s.phenomenon}</span>{" "}
                  <span
                    className="v-faint"
                    style={{ fontSize: "var(--text-tiny)" }}
                  >
                    — {s.detail}
                  </span>
                </span>
                <span className="v-badge" data-conf="tight">
                  {s.class}
                </span>
              </div>
            ))}
          </div>
        </section>
      )}

      <p className="v-faint" style={{ fontSize: "var(--text-tiny)" }}>
        {v.class} · {v.gateNote}
      </p>
    </div>
  );
}

// One impacted-caller ← degraded-callee link. The MEASURED observed-flow edge
// and the AUTHORED "why" are shown ADJACENTLY, each labelled — never fused into
// a causal sentence (doc 01 §5).
function Link({ l }: { l: CrossServiceLink }) {
  return (
    <article className="v-prov-card" data-prov="MEASURED">
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "var(--space-sm)",
          flexWrap: "wrap",
        }}
      >
        <strong className="v-mono">{l.impacted}</strong>
        <span aria-hidden className="v-faint">
          ← calls →
        </span>
        <strong className="v-mono">{l.degraded}</strong>
        <span
          className="v-badge"
          data-conf="tight"
          title="MEASURED observed flow"
        >
          {l.edge_class}
        </span>
        <span
          className="v-badge"
          data-conf={l.edge_traversal === "valid" ? "tight" : "wide"}
          title="edge validity contract (doc 07 §3.2)"
        >
          {l.edge_traversal}
        </span>
      </div>
      <p className="v-authored-note">
        per the graph ({l.temporal} · {l.author} {l.version}): {l.why}
      </p>
    </article>
  );
}
