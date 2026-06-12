import { useState } from "react";
import { ProvChip } from "../components/Brand";
import {
  type CascadeCard,
  type InsightCard,
  type InsightsView,
  type MemberRow,
  entityLabel,
  shortMetric,
} from "./types";
import { useApi } from "./useApi";

// The Insight feed (doc 10 M2 / §3.1) — the "now" surface. Each card is a
// MEASURED phenomenon match with its AUTHORED member/relation notes attached
// ADJACENTLY (never fused into a causal sentence). Full/degraded marks always
// visible; every claim drills down to the fingerprint evidence.

export function InsightFeed() {
  const { data, error, loaded } = useApi<InsightsView>("/api/insights");
  if (!loaded) return <p className="v-muted">Loading insights…</p>;
  if (error && !data)
    return (
      <div className="v-card" style={{ borderColor: "var(--status-error)" }}>
        <h2>Insights unavailable</h2>
        <p className="v-muted">Could not reach the obsd API ({error}).</p>
      </div>
    );
  if (!data) return null;
  const v = data;
  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-lg)",
      }}
    >
      <Summary v={v} />
      {v.cascades.length > 0 && (
        <section>
          <h3
            className="v-overline"
            style={{ marginBottom: "var(--space-sm)" }}
          >
            Cascade stories — authored relations currently manifest
          </h3>
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              gap: "var(--space-sm)",
            }}
          >
            {v.cascades.map((c, i) => (
              <CascadeRow
                key={`${c.trigger.phenomenon}-${c.downstream.phenomenon}-${i}`}
                c={c}
              />
            ))}
          </div>
        </section>
      )}
      <section>
        <h3 className="v-overline" style={{ marginBottom: "var(--space-sm)" }}>
          Matched now — {v.findings.length}
        </h3>
        {v.findings.length === 0 ? (
          <div className="v-card">
            <p className="v-muted">
              No phenomena matched this evaluation window. The cluster is quiet
              on every curated pattern — a silent feed is an honest one.
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
            {v.findings.map((f, i) => (
              <FindingCard key={`${f.entityCei}-${f.phenomenon}-${i}`} f={f} />
            ))}
          </div>
        )}
      </section>
    </div>
  );
}

function Summary({ v }: { v: InsightsView }) {
  const s = v.summary;
  const items: [string, number | string][] = [
    ["matched", s.total],
    ["full", s.full],
    ["degraded", s.degraded],
    ["1-hop", s.firstOrder],
    ["2-hop", s.secondOrder],
    ["cascades", s.cascades],
    ["at-risk", s.withAtRisk],
  ];
  return (
    <div
      className="v-card"
      style={{ display: "flex", flexWrap: "wrap", gap: "var(--space-lg)" }}
    >
      {items.map(([label, n]) => (
        <div key={label}>
          <div
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: "var(--text-h2)",
              color: "var(--text-strong)",
            }}
          >
            {n}
          </div>
          <div className="v-overline">{label}</div>
        </div>
      ))}
    </div>
  );
}

function CascadeRow({ c }: { c: CascadeCard }) {
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
        <span className="v-mono">{c.trigger.phenomenon}</span>
        <span aria-hidden style={{ color: "var(--text-faint)" }}>
          ⛓ →
        </span>
        <span className="v-mono">{c.downstream.phenomenon}</span>
        <span className="v-badge" data-span="">
          {c.temporal}
        </span>
      </div>
      <p
        className="v-muted"
        style={{ marginTop: "var(--space-xs)", fontSize: "var(--text-small)" }}
      >
        {entityLabel(c.trigger.entityCei)} · related: {c.related}
      </p>
      <span className="v-authored-note">per the graph: {c.why}</span>
      <p
        className="v-faint"
        style={{ marginTop: "var(--space-xs)", fontSize: "var(--text-tiny)" }}
      >
        Two MEASURED co-occurrences joined by an AUTHORED relation — adjacent,
        never a causal proof.
      </p>
    </article>
  );
}

function FindingCard({ f }: { f: InsightCard }) {
  const [open, setOpen] = useState(false);
  return (
    <article className="v-prov-card" data-prov="MEASURED">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        style={{
          all: "unset",
          boxSizing: "border-box",
          width: "100%",
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: "var(--space-sm)",
          cursor: "pointer",
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
          <span aria-hidden style={{ color: "var(--text-faint)" }}>
            {open ? "▾" : "▸"}
          </span>
          <strong style={{ fontFamily: "var(--font-heading)" }}>
            {f.label}
          </strong>
          <span className="v-badge" data-q={f.quality}>
            {f.quality}
          </span>
          <span className="v-badge" data-span="">
            {f.span}
          </span>
          {f.blastRadius.length > 0 && (
            <span className="v-badge" data-span="">
              {f.blastRadius.length} at-risk
            </span>
          )}
        </div>
        <ProvChip cls="MEASURED" />
      </button>
      <p
        className="v-muted"
        style={{ marginTop: "var(--space-xs)", fontSize: "var(--text-small)" }}
      >
        {entityLabel(f.entityCei)} · {f.requiredMet}/{f.requiredTotal} required
        met
        {" · "}
        {(f.completeness * 100).toFixed(0)}% complete
      </p>
      {open && (
        <div style={{ marginTop: "var(--space-sm)" }}>
          <Evidence f={f} />
        </div>
      )}
    </article>
  );
}

function Evidence({ f }: { f: InsightCard }) {
  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-sm)",
      }}
    >
      <div>
        <div className="v-overline">
          Members (MEASURED states · authored notes)
        </div>
        {f.members.map((m, i) => (
          <MemberLine key={`${m.metric}-${m.neighbour}-${i}`} m={m} />
        ))}
        {f.unobservable.map((u) => (
          <div key={u} className="v-evi">
            <span className="v-faint">unobservable</span>
            <span className="v-faint">{u}</span>
            <span />
          </div>
        ))}
      </div>

      {f.spanPath.length > 0 && (
        <div>
          <div className="v-overline">Span path (edge validity)</div>
          {f.spanPath.map((s) => (
            <div key={`${s.type}-${s.from}-${s.to}`} className="v-evi">
              <span className="v-mono">{s.type}</span>
              <span className="v-mono v-muted">
                {entityLabel(s.from)} → {entityLabel(s.to)}
              </span>
              <span
                className="v-edge-line"
                data-status={s.result}
                title={s.result}
              />
            </div>
          ))}
          {f.suspectEdges.map((e) => (
            <p
              key={e}
              className="v-faint"
              style={{
                fontSize: "var(--text-tiny)",
                color: "var(--rung-at-threshold)",
              }}
            >
              ⚠ degraded by suspect edge: {e}
            </p>
          ))}
        </div>
      )}

      {f.blastRadius.length > 0 && (
        <div>
          <div className="v-overline">
            Blast radius — at risk per the graph (AUTHORED)
          </div>
          {f.blastRadius.map((r) => (
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
        graph {f.graphVersion.replace("sha256:", "").slice(0, 12)}… · MEASURED
        match, AUTHORED notes adjacent, never fused.
      </p>
    </div>
  );
}

function MemberLine({ m }: { m: MemberRow }) {
  return (
    <div className="v-evi">
      <span className="v-faint" title={m.temporal}>
        {m.role}
        {m.temporal ? ` ${m.temporal}` : ""}
      </span>
      <span>
        <span className="v-mono">
          {shortMetric(m.metric)} = {m.state}
          {m.barFlagged ? " (default bar)" : ""}
        </span>
        {m.neighbour && (
          <span className="v-faint" style={{ fontSize: "var(--text-tiny)" }}>
            {" "}
            ⤷ {entityLabel(m.neighbour)} via {m.via} [{m.edgeResult}]
          </span>
        )}
        {m.note && (
          <span className="v-authored-note">per the graph: {m.note}</span>
        )}
      </span>
      <span />
    </div>
  );
}
