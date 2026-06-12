import { useMemo, useState } from "react";
import {
  type TopologyView as TopoData,
  type TopoEdge,
  type TopoNode,
  entityLabel,
} from "./types";
import { useApi } from "./useApi";

// The Topology view (doc 10 M3 / §3.2) — the bound customer graph rendered live.
// Entities as nodes (grouped by kind into deterministic columns — no physics, so
// the layout is stable and testable), valid edges as solid links, suspect edges
// visibly distinct (dashed). CURRENT-condition marks only: matched (teal ring)
// and loud (amber dashed); predictive marks (10 M5) are a SEPARATE visual
// language — a violet DIAMOND beside the node, never the circle family — so
// "is" and "might" cannot be confused at a glance.

const KIND_ORDER = [
  "Node",
  "Pod",
  "Container",
  "PersistentVolumeClaim",
  "Service",
];
const COL_W = 230;
const ROW_H = 46;
const PAD_X = 24;
const PAD_Y = 56;

export function TopologyView() {
  const { data, error, loaded } = useApi<TopoData>("/api/topology");
  const [sel, setSel] = useState<string | null>(null);

  const layout = useMemo(
    () => (data ? computeLayout(data.nodes) : null),
    [data],
  );

  if (!loaded) return <p className="v-muted">Loading topology…</p>;
  if (error && !data)
    return (
      <div className="v-card" style={{ borderColor: "var(--status-error)" }}>
        <h2>Topology unavailable</h2>
        <p className="v-muted">Could not reach the obsd API ({error}).</p>
      </div>
    );
  if (!data || !layout) return null;
  const v = data;
  const pos = layout;
  const selNode = sel ? (v.nodes.find((n) => n.ceiKey === sel) ?? null) : null;

  const width = pos.columns.length * COL_W + PAD_X * 2;
  const height = Math.max(pos.maxRows * ROW_H + PAD_Y * 2, 240);

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-md)",
      }}
    >
      <Legend v={v} />
      <div
        className="v-card"
        style={{ overflowX: "auto", padding: "var(--space-sm)" }}
      >
        <svg
          width={width}
          height={height}
          role="img"
          aria-label="cluster topology with current-condition marks"
          style={{ display: "block" }}
        >
          {/* column headers */}
          {pos.columns.map((c, i) => (
            <text
              key={c}
              x={PAD_X + i * COL_W + 70}
              y={28}
              fill="var(--text-muted)"
              fontFamily="var(--font-heading)"
              fontSize={12}
            >
              {c}
            </text>
          ))}
          {/* edges first (under the nodes) */}
          {v.edges.map((e) => {
            const a = pos.at[e.from];
            const b = pos.at[e.to];
            if (!a || !b) return null;
            return (
              <EdgeLine key={`${e.type}-${e.from}-${e.to}`} e={e} a={a} b={b} />
            );
          })}
          {/* nodes */}
          {v.nodes.map((n) => {
            const p = pos.at[n.ceiKey];
            if (!p) return null;
            return (
              <NodeGlyph
                key={n.ceiKey}
                n={n}
                x={p.x}
                y={p.y}
                active={sel === n.ceiKey}
                onClick={() => setSel(sel === n.ceiKey ? null : n.ceiKey)}
              />
            );
          })}
        </svg>
      </div>
      {selNode && <NodeDetail n={selNode} edges={v.edges} />}
      {v.truncated > 0 && (
        <p className="v-faint" style={{ fontSize: "var(--text-tiny)" }}>
          {v.truncated} more entities omitted past the render cap (stated, never
          silent).
        </p>
      )}
    </div>
  );
}

interface Pt {
  x: number;
  y: number;
}
interface Layout {
  columns: string[];
  at: Record<string, Pt>;
  maxRows: number;
}

function computeLayout(nodes: TopoNode[]): Layout {
  const byKind = new Map<string, TopoNode[]>();
  for (const n of nodes) {
    const arr = byKind.get(n.kind) ?? [];
    arr.push(n);
    byKind.set(n.kind, arr);
  }
  const kinds = [...byKind.keys()].sort((a, b) => {
    const ia = KIND_ORDER.indexOf(a);
    const ib = KIND_ORDER.indexOf(b);
    return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib) || a.localeCompare(b);
  });
  const at: Record<string, Pt> = {};
  let maxRows = 0;
  kinds.forEach((kind, col) => {
    const list = (byKind.get(kind) ?? []).sort((a, b) =>
      a.ceiKey.localeCompare(b.ceiKey),
    );
    maxRows = Math.max(maxRows, list.length);
    list.forEach((n, row) => {
      at[n.ceiKey] = { x: PAD_X + col * COL_W, y: PAD_Y + row * ROW_H };
    });
  });
  return { columns: kinds, at, maxRows };
}

function EdgeLine({ e, a, b }: { e: TopoEdge; a: Pt; b: Pt }) {
  const x1 = a.x + 180;
  const y1 = a.y + 14;
  const x2 = b.x;
  const y2 = b.y + 14;
  const stroke =
    e.status === "valid"
      ? "var(--prov-measured-on-dark, var(--prov-measured))"
      : e.status === "suspect"
        ? "var(--rung-at-threshold)"
        : "var(--text-faint, var(--text-muted))";
  return (
    <line
      x1={x1}
      y1={y1}
      x2={x2}
      y2={y2}
      stroke={stroke}
      strokeWidth={1.5}
      strokeDasharray={
        e.status === "valid"
          ? undefined
          : e.status === "suspect"
            ? "5 4"
            : "2 3"
      }
      opacity={0.7}
    >
      <title>
        {e.type}: {e.status}
      </title>
    </line>
  );
}

function NodeGlyph({
  n,
  x,
  y,
  active,
  onClick,
}: {
  n: TopoNode;
  x: number;
  y: number;
  active: boolean;
  onClick: () => void;
}) {
  const markKind = n.matched
    ? "matched"
    : n.loud
      ? "loud"
      : n.selected
        ? "selected"
        : "quiet";
  const stroke = n.matched
    ? "var(--prov-measured-on-dark, var(--prov-measured))"
    : n.loud
      ? "var(--rung-at-threshold)"
      : "var(--border-soft)";
  const markFill = {
    matched: "var(--prov-measured-on-dark, var(--prov-measured))",
    loud: "var(--rung-at-threshold)",
    selected: "var(--brand-azure)",
    quiet: "var(--border-strong, var(--border-soft))",
  }[markKind];
  return (
    // A focusable, keyboard-operable SVG group (SVG cannot host a <button>); the
    // aria-label announces the node and its current mark.
    <g
      transform={`translate(${x},${y})`}
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onClick();
        }
      }}
      tabIndex={0}
      aria-label={`${n.kind} ${n.name}: ${markKind}`}
      style={{ cursor: "pointer" }}
    >
      <rect
        width={180}
        height={28}
        rx={6}
        fill="var(--surface-2)"
        stroke={stroke}
        strokeWidth={active ? 2 : 1}
        strokeDasharray={n.loud && !n.matched ? "5 4" : undefined}
      />
      <circle cx={14} cy={14} r={5} fill={markFill}>
        <title>{markKind}</title>
      </circle>
      {n.warned && (
        <polygon
          points="170,6 176,14 170,22 164,14"
          fill="var(--prov-projected-band)"
          stroke="var(--prov-projected-on-dark, var(--prov-projected))"
          strokeWidth={1.5}
        >
          <title>early warning (PROJECTED)</title>
        </polygon>
      )}
      <text
        x={26}
        y={18}
        fill="var(--text-strong)"
        fontFamily="var(--font-mono)"
        fontSize={11}
      >
        {n.name.length > 22 ? `${n.name.slice(0, 21)}…` : n.name}
      </text>
    </g>
  );
}

function Legend({ v }: { v: TopoData }) {
  const item = (kind: string, label: string, count: number) => (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
      <span className="v-topo-mark" data-kind={kind} />
      <span className="v-muted" style={{ fontSize: "var(--text-small)" }}>
        {label} {count}
      </span>
    </span>
  );
  return (
    <div
      className="v-card"
      style={{
        display: "flex",
        flexWrap: "wrap",
        gap: "var(--space-lg)",
        alignItems: "center",
      }}
    >
      {item("matched", "matched (is)", v.summary.matched)}
      {item("loud", "loud / unexplained", v.summary.loud)}
      {item("warned", "projected (might)", v.summary.warned)}
      {item("selected", "watched", v.summary.selected)}
      <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
        <span className="v-edge-line" data-status="valid" />
        <span className="v-muted" style={{ fontSize: "var(--text-small)" }}>
          valid {v.summary.validEdges}
        </span>
      </span>
      <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
        <span className="v-edge-line" data-status="suspect" />
        <span className="v-muted" style={{ fontSize: "var(--text-small)" }}>
          suspect {v.summary.suspectEdges}
        </span>
      </span>
      <span className="v-faint" style={{ fontSize: "var(--text-tiny)" }}>
        "is" marks are circles; the "might" mark (PROJECTED) is the violet
        diamond — separate visual languages, never confused.
      </span>
    </div>
  );
}

function NodeDetail({ n, edges }: { n: TopoNode; edges: TopoEdge[] }) {
  const links = edges.filter((e) => e.from === n.ceiKey || e.to === n.ceiKey);
  return (
    <article className="v-prov-card" data-prov="MEASURED">
      <header
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
        }}
      >
        <strong style={{ fontFamily: "var(--font-heading)" }}>
          {entityLabel(n.ceiKey)}
        </strong>
        <span style={{ display: "flex", gap: 6 }}>
          {n.matched && (
            <span className="v-badge" data-q={n.degraded ? "degraded" : "full"}>
              matched
            </span>
          )}
          {n.loud && (
            <span className="v-badge" data-span="">
              loud
            </span>
          )}
          {n.selected && (
            <span className="v-badge" data-span="">
              watched
            </span>
          )}
        </span>
      </header>
      {n.phenomena.length > 0 && (
        <p
          className="v-mono"
          style={{
            marginTop: "var(--space-xs)",
            fontSize: "var(--text-small)",
          }}
        >
          {n.phenomena.join(", ")}
        </p>
      )}
      <div className="v-overline" style={{ marginTop: "var(--space-sm)" }}>
        Edges
      </div>
      {links.length === 0 ? (
        <p className="v-faint" style={{ fontSize: "var(--text-tiny)" }}>
          no topology edges
        </p>
      ) : (
        links.map((e) => (
          <div key={`${e.type}-${e.from}-${e.to}`} className="v-evi">
            <span className="v-mono">{e.type}</span>
            <span className="v-mono v-muted">
              {e.from === n.ceiKey
                ? `→ ${entityLabel(e.to)}`
                : `← ${entityLabel(e.from)}`}
            </span>
            <span
              className="v-edge-line"
              data-status={e.status}
              title={e.status}
            />
          </div>
        ))
      )}
    </article>
  );
}
