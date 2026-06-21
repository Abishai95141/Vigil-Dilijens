import { plane, signal } from "@/lib/tokens";

// ─────────────────────────────────────────────────────────────────────────────
// The cluster graph's visual language — Neo4j-grade node-link clarity, rendered
// in the Vigil brand plane. The discipline holds: COLOR is reserved for
// operational STATE (success/warning/error/info); SHAPE + SIZE + DASH carry the
// rest (node kind, edge relation, provenance). The white fill ("now", Signal/000)
// is reserved for the single focused node. Labels sit below the node with a faint
// plane-colored halo so they stay legible under density; zoom-based level-of-
// detail (driven from ClusterGraph) hides captions and edge labels when zoomed out.
// cytoscape validates the stylesheet at runtime; its static prop typing is
// over-strict for the selectors we use, so we type loosely.
// ─────────────────────────────────────────────────────────────────────────────
type CyStyle = { selector: string; style: Record<string, string | number> };

const LABEL_FONT = "Geist, Inter, system-ui, sans-serif";
const MONO_FONT = "JetBrains Mono, monospace";

export const cyStyle: CyStyle[] = [
  // ── namespace compound groups (subtle container, label top-left) ──
  {
    selector: "node.group",
    style: {
      "background-color": plane.surface,
      "background-opacity": 0.35,
      "border-color": plane.rule,
      "border-width": 1,
      "border-style": "solid",
      shape: "round-rectangle",
      label: "data(label)",
      color: plane.inkLow,
      "font-family": MONO_FONT,
      "font-size": 10,
      "font-weight": 500,
      "text-transform": "uppercase",
      "text-valign": "top",
      "text-halign": "left",
      "text-margin-x": 12,
      "text-margin-y": 16,
      "letter-spacing": 1.5,
      padding: 36, // more inner air so nodes never crowd the container edge
      "z-index": 1,
    },
  },
  { selector: "node.group:active", style: { "overlay-opacity": 0 } },

  // ── entity nodes (base = a workload) — bigger, Neo4j-like discs ──
  {
    selector: "node.entity",
    style: {
      // size scales with connection degree (set per node in data.diam)
      width: "data(diam)",
      height: "data(diam)",
      shape: "ellipse",
      "background-color": plane.surfaceHi,
      "background-opacity": 1,
      "border-width": 2,
      "border-color": plane.inkLow,
      label: "data(label)",
      color: plane.inkSoft,
      "font-family": LABEL_FONT,
      "font-size": 11.5,
      "font-weight": 500,
      "text-valign": "bottom",
      "text-halign": "center",
      "text-margin-y": 8, // more gap node→caption so the disc + label read as distinct
      "text-max-width": "132px",
      "text-wrap": "ellipsis",
      "text-background-color": plane.plane,
      "text-background-opacity": 0.86, // crisper caption against the busy canvas
      "text-background-padding": 3.5,
      "text-background-shape": "round-rectangle",
      "min-zoomed-font-size": 9, // LOD: caption fades out when zoomed far out
      "z-index": 10,
      "transition-property": "border-color, background-color, width, height, border-width",
      "transition-duration": 140,
    },
  },
  // kind glyphs (shape carries meaning, never color):
  //   k8s Node       → rounded square (infrastructure)
  //   DaemonSet      → hexagon (one-per-node system workload)
  //   StaticPod      → diamond (control-plane static)
  //   Pod (expanded) → small circle
  {
    selector: 'node.entity[kind = "Node"]',
    style: { shape: "round-rectangle", "border-width": 2 },
  },
  { selector: 'node.entity[kind = "DaemonSet"]', style: { shape: "hexagon" } },
  { selector: 'node.entity[kind = "StaticPod"]', style: { shape: "diamond" } },
  // Service (routing endpoint) → tag · PVC (storage) → barrel. Secondary layers,
  // styled neutral (they carry no phenomenon state) so they never read as "healthy".
  {
    selector: 'node.entity[kind = "Service"]',
    style: { shape: "tag", width: 17, height: 17, "background-color": plane.surface },
  },
  {
    selector: 'node.entity[kind = "PVC"]',
    style: { shape: "barrel", width: 17, height: 17, "background-color": plane.surface },
  },
  {
    selector: "node.entity.secondary",
    style: { "border-color": plane.mute, "background-color": plane.surface, color: plane.inkLow },
  },
  {
    selector: "node.pod",
    style: {
      width: 9,
      height: 9,
      shape: "ellipse",
      "background-color": plane.surfaceHi,
      "border-width": 1.2,
      "border-color": plane.mute,
      label: "data(label)",
      color: plane.inkLow,
      "font-family": MONO_FONT,
      "font-size": 8,
      "text-valign": "bottom",
      "text-margin-y": 2,
      "min-zoomed-font-size": 11,
      "z-index": 9,
    },
  },

  // ── health marks — COLOR = operational STATE only ──
  { selector: "node.sev-ok", style: { "border-color": signal.success } },
  {
    selector: "node.sev-info",
    style: { "border-color": signal.info, "background-color": signal.infoTint },
  },
  {
    selector: "node.sev-degraded",
    style: { "border-color": signal.warning, "background-color": signal.warningTint },
  },
  {
    selector: "node.sev-firing",
    style: {
      "border-color": signal.error,
      "background-color": signal.error,
      color: plane.ink,
      "border-width": 2.5,
    },
  },
  // "warned" = a PROJECTED early warning: a dashed ring ("might", a separate language)
  {
    selector: "node.warned",
    style: { "border-style": "dashed", "border-color": signal.info },
  },

  // ── interaction states ──
  // hover halo (Neo4j-style ring on the node under the cursor)
  {
    selector: "node.entity.hover",
    style: {
      "border-width": 3,
      "overlay-color": plane.signal,
      "overlay-opacity": 0.05,
      "overlay-padding": 6,
    },
  },
  // focused / selected = the white-filled "now"
  {
    selector: "node.entity.focus",
    style: {
      "background-color": plane.signal,
      "border-color": plane.signal,
      color: plane.signal,
      "border-width": 2.5,
      "z-index": 30,
    },
  },
  // soft neighbor highlight on hover (Neo4j shows a node's neighbors on hover)
  { selector: "node.focus-soft", style: { "border-width": 2.5, "z-index": 20 } },
  // on a highlighted cascade/root-cause path
  {
    selector: "node.path",
    style: { "border-color": plane.signal, "border-width": 3, "z-index": 28 },
  },
  // dimmed (out of the focused neighborhood / search)
  { selector: "node.dim", style: { opacity: 0.12, "text-opacity": 0.12 } },
  { selector: "node.hidden", style: { display: "none" } },

  // ── edges ──
  {
    selector: "edge",
    style: {
      width: 1.1,
      "line-color": plane.rule,
      "curve-style": "bezier",
      "target-arrow-color": plane.ruleStrong,
      "target-arrow-shape": "none",
      "font-family": MONO_FONT,
      "font-size": 8,
      color: plane.inkLow,
      "text-background-color": plane.plane,
      "text-background-opacity": 0.85,
      "text-background-padding": 2,
      "min-zoomed-font-size": 16, // LOD: edge labels only when zoomed in (calmer overview)
      opacity: 0.85,
      "z-index": 2,
    },
  },
  // dependency (observed flow) = the call graph: directed, brighter, labelled "calls"
  {
    selector: 'edge[etype = "flow"]',
    style: {
      width: 1.6,
      "line-color": plane.inkLow,
      "target-arrow-shape": "triangle-backcurve",
      "target-arrow-color": plane.inkLow,
      "arrow-scale": 1,
      label: "data(rel)",
      "text-rotation": "autorotate",
    },
  },
  // placement (runs-on) = infrastructure: faint, undirected, recedes
  {
    selector: 'edge[etype = "runs-on"]',
    style: { width: 0.8, "line-color": plane.rule, "line-style": "dotted", opacity: 0.5 },
  },
  // service routing (selects) = which workload a Service fronts: thin, dashed
  {
    selector: 'edge[etype = "selects"]',
    style: { width: 0.9, "line-color": plane.mute, "line-style": "dashed", opacity: 0.6 },
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
  { selector: 'edge[status = "retracted"]', style: { opacity: 0.22, "line-style": "dotted" } },

  // edge interaction states
  {
    selector: "edge.hl",
    style: {
      width: 2.4,
      "line-color": plane.signal,
      "target-arrow-color": plane.signal,
      opacity: 1,
      "z-index": 25,
    },
  },
  {
    selector: "edge.path",
    style: {
      width: 2.6,
      "line-color": signal.error,
      "target-arrow-color": signal.error,
      opacity: 1,
      "z-index": 26,
    },
  },
  { selector: "edge.dim", style: { opacity: 0.05 } },
  { selector: "edge.hidden", style: { display: "none" } },

  // ── cross-layer affordance ──────────────────────────────────────────────────
  // A node connected ONLY in a toggled-off layer (a Service routes to it, or it mounts a
  // PVC, or it runs on a Node) is NOT orphaned — a dashed ring marks it so "no edge in this
  // view" never reads as "disconnected". DASH carries the meaning; the state colour is kept.
  {
    selector: "node.entity.cross-layer",
    style: { "border-style": "dashed", "border-opacity": 0.85 },
  },
  // Reveal-on-select: the selected node's cross-layer connections are ghosted in (its edges
  // + the endpoints pulled from a hidden layer) so the link shows on demand without
  // permanently cluttering the dependency mesh. Faint + dashed = "shown from another layer".
  // Placed last so they win over .dim / .hl for a revealed element.
  {
    selector: "node.ghost",
    style: { opacity: 0.5, "text-opacity": 0.5, "border-style": "dashed" },
  },
  {
    selector: "edge.ghost",
    style: {
      "line-style": "dashed",
      "line-color": plane.mute,
      "target-arrow-color": plane.mute,
      "target-arrow-shape": "triangle-backcurve",
      "arrow-scale": 0.7,
      opacity: 0.55,
      width: 1.1,
      "z-index": 3,
    },
  },
];
