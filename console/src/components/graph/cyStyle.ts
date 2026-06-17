import { plane, signal } from "@/lib/tokens";

// The graph's visual language, in the brand plane. Color is reserved for state;
// shape + fill + dash carry meaning alongside hue. The white fill ("now") is
// reserved for the focused/selected node — the single observed node.
// Typed loosely: cytoscape validates the stylesheet at runtime, and its static
// property typing is over-strict for the selectors/props we use here.
type CyStyle = { selector: string; style: Record<string, string | number> };
export const cyStyle: CyStyle[] = [
  // ── namespace compound groups ──
  {
    selector: "node.group",
    style: {
      "background-color": plane.surface,
      "background-opacity": 0.45,
      "border-color": plane.rule,
      "border-width": 1,
      shape: "round-rectangle",
      label: "data(label)",
      color: plane.inkLow,
      "font-family": "JetBrains Mono, monospace",
      "font-size": 9,
      "text-transform": "uppercase",
      "text-valign": "top",
      "text-halign": "left",
      "text-margin-x": 8,
      "text-margin-y": 12,
      padding: 18,
      "z-index": 1,
    },
  },
  // ── entity nodes (base) ──
  {
    selector: "node.entity",
    style: {
      width: 13,
      height: 13,
      shape: "ellipse",
      "background-color": plane.surfaceHi,
      "background-opacity": 1,
      "border-width": 1.4,
      "border-color": plane.inkLow,
      label: "data(label)",
      color: plane.inkMid,
      "font-family": "Geist, sans-serif",
      "font-size": 8.5,
      "text-valign": "bottom",
      "text-halign": "center",
      "text-margin-y": 3,
      "text-max-width": "90px",
      "text-wrap": "ellipsis",
      "min-zoomed-font-size": 7,
      "z-index": 10,
      "transition-property": "border-color, background-color, width, height",
      "transition-duration": 150,
    },
  },
  // kind: a k8s Node is a square; a Pod is a circle (shape carries meaning)
  {
    selector: 'node.entity[kind = "Node"]',
    style: { shape: "round-rectangle", width: 16, height: 16 },
  },

  // ── health marks (color reserved for state) ──
  { selector: "node.sev-ok", style: { "border-color": signal.success } },
  {
    selector: "node.sev-info",
    style: { "border-color": signal.info, "background-color": signal.infoTint },
  },
  {
    selector: "node.sev-degraded",
    style: {
      "border-color": signal.warning,
      "background-color": signal.warningTint,
      width: 15,
      height: 15,
    },
  },
  {
    selector: "node.sev-firing",
    style: {
      "border-color": signal.error,
      "background-color": signal.error,
      width: 17,
      height: 17,
      color: plane.ink,
    },
  },
  // "warned" = a PROJECTED early warning: a dashed outer ring ("might", separate language)
  { selector: "node.warned", style: { "border-style": "dashed", "border-color": signal.info } },
  // focused / selected = the white-filled "now"
  {
    selector: "node.entity.focus",
    style: {
      "background-color": plane.signal,
      "border-color": plane.signal,
      color: plane.signal,
      "z-index": 30,
    },
  },
  // dimmed (out of the focused neighborhood)
  { selector: "node.dim", style: { opacity: 0.16 } },
  { selector: "node.hidden", style: { display: "none" } },

  // ── edges ──
  {
    selector: "edge",
    style: {
      width: 1,
      "line-color": plane.rule,
      "curve-style": "bezier",
      "target-arrow-color": plane.ruleStrong,
      "target-arrow-shape": "none",
      opacity: 0.9,
      "z-index": 2,
    },
  },
  // dependency (observed flow) edges are directed + brighter
  {
    selector: 'edge[etype = "flow"]',
    style: {
      width: 1.2,
      "line-color": plane.inkLow,
      "target-arrow-shape": "triangle-backcurve",
      "target-arrow-color": plane.inkLow,
      "arrow-scale": 0.7,
    },
  },
  // suspect edges: dashed + amber (detection degrades across them — never silent)
  {
    selector: 'edge[status = "suspect"]',
    style: {
      "line-style": "dashed",
      "line-color": signal.warning,
      "target-arrow-color": signal.warning,
    },
  },
  { selector: 'edge[status = "retracted"]', style: { opacity: 0.25, "line-style": "dotted" } },
  // highlighted (a focused node's incident edges)
  {
    selector: "edge.hl",
    style: {
      width: 2,
      "line-color": plane.signal,
      "target-arrow-color": plane.signal,
      opacity: 1,
      "z-index": 25,
    },
  },
  { selector: "edge.dim", style: { opacity: 0.06 } },
  { selector: "edge.hidden", style: { display: "none" } },
];
