// GraphMinimap — a lightweight, dependency-free overview of the whole graph with
// a draggable viewport rectangle (the Neo4j/IDE minimap pattern). Reads node
// positions from the live cytoscape Core and re-renders on pan/zoom/layout; click
// or drag inside it pans the main graph. Kept generic: it draws whatever nodes
// exist, so it scales to any cluster. SVG only — no extra cytoscape plugins.

import { plane, severityColor } from "@/lib/tokens";
import type { Core, NodeSingular } from "cytoscape";
import { useEffect, useRef, useState } from "react";

const W = 168;
const H = 116;
const PAD = 6;

type Box = { x1: number; y1: number; x2: number; y2: number };

export function GraphMinimap({ cy }: { cy: Core | null }) {
  const [, force] = useState(0);
  const svgRef = useRef<SVGSVGElement>(null);
  const dragging = useRef(false);

  // re-render the minimap whenever the graph moves or changes
  useEffect(() => {
    if (!cy) return;
    const tick = () => force((n) => n + 1);
    cy.on("pan zoom render position add remove", tick);
    return () => {
      cy.off("pan zoom render position add remove", tick);
    };
  }, [cy]);

  if (!cy || cy.nodes(".entity").length === 0) return null;

  // model-space bounding box of all real nodes (exclude compound groups)
  const nodes = cy.nodes(".entity, .pod").filter((n) => !n.hasClass("hidden"));
  if (nodes.length === 0) return null;
  const bb = nodes.boundingBox({ includeLabels: false });
  const bw = Math.max(bb.w, 1);
  const bh = Math.max(bb.h, 1);
  const scale = Math.min((W - 2 * PAD) / bw, (H - 2 * PAD) / bh);
  const offX = PAD + (W - 2 * PAD - bw * scale) / 2;
  const offY = PAD + (H - 2 * PAD - bh * scale) / 2;
  const toMini = (mx: number, my: number) => ({
    x: offX + (mx - bb.x1) * scale,
    y: offY + (my - bb.y1) * scale,
  });
  // inverse: a point on the minimap → model coordinates
  const toModel = (px: number, py: number) => ({
    x: bb.x1 + (px - offX) / scale,
    y: bb.y1 + (py - offY) / scale,
  });

  // current viewport in model space → minimap rectangle
  const ext = cy.extent();
  const a = toMini(ext.x1, ext.y1);
  const b = toMini(ext.x2, ext.y2);
  const view: Box = {
    x1: Math.max(0, Math.min(a.x, b.x)),
    y1: Math.max(0, Math.min(a.y, b.y)),
    x2: Math.min(W, Math.max(a.x, b.x)),
    y2: Math.min(H, Math.max(a.y, b.y)),
  };

  function panTo(clientX: number, clientY: number) {
    if (!cy || !svgRef.current) return;
    const r = svgRef.current.getBoundingClientRect();
    const m = toModel(clientX - r.left, clientY - r.top);
    const z = cy.zoom();
    // center the main viewport on the clicked model point
    cy.pan({
      x: cy.width() / 2 - m.x * z,
      y: cy.height() / 2 - m.y * z,
    });
  }

  return (
    <div className="absolute right-3 bottom-3 z-20 overflow-hidden rounded-[8px] border border-rule bg-plane/85 backdrop-blur">
      <div className="border-b border-rule px-2 py-1 font-mono text-[8.5px] tracking-[0.1em] text-ink-low uppercase">
        overview
      </div>
      <svg
        ref={svgRef}
        width={W}
        height={H}
        className="block cursor-crosshair"
        aria-label="graph minimap"
        onMouseDown={(e) => {
          dragging.current = true;
          panTo(e.clientX, e.clientY);
        }}
        onMouseMove={(e) => {
          if (dragging.current) panTo(e.clientX, e.clientY);
        }}
        onMouseUp={() => {
          dragging.current = false;
        }}
        onMouseLeave={() => {
          dragging.current = false;
        }}
      >
        <title>Graph overview — drag to pan</title>
        {nodes.map((n: NodeSingular) => {
          const p = n.position();
          const m = toMini(p.x, p.y);
          const sev = (n.data("sev") as Parameters<typeof severityColor>[0]) ?? "neutral";
          return (
            <circle
              key={n.id()}
              cx={m.x}
              cy={m.y}
              r={n.hasClass("pod") ? 0.9 : 1.7}
              fill={sev === "ok" || !sev ? plane.inkLow : severityColor(sev)}
              opacity={0.9}
            />
          );
        })}
        <rect
          x={view.x1}
          y={view.y1}
          width={Math.max(view.x2 - view.x1, 2)}
          height={Math.max(view.y2 - view.y1, 2)}
          fill={plane.signal}
          fillOpacity={0.08}
          stroke={plane.signal}
          strokeOpacity={0.55}
          strokeWidth={1}
        />
      </svg>
    </div>
  );
}
