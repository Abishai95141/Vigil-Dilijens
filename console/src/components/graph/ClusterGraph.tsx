// The cluster graph — the headline surface. A Neo4j-grade node-link view of the
// bound customer graph: workloads + their service-to-service call graph by
// default, with the service-routing (pod-to-service), infrastructure (placement)
// and storage layers toggled on demand. Interaction follows Neo4j conventions —
// hover to highlight a node's relationships, click to inspect, double-click to
// isolate a neighborhood, drag to reposition, scroll to zoom (with label
// level-of-detail). Generic + scalable: nothing is boutique-specific, layers and
// the minimap keep it readable as the cluster grows.

import { useTopology } from "@/api/client";
import type { TopoNode, TopologyView } from "@/api/types";
import { Icon } from "@/components/ui/icons";
import { Button, Mono, Spinner, StateDot, cx } from "@/components/ui/primitives";
import { type Severity, plane, signal } from "@/lib/tokens";
import cytoscape, { type Core, type ElementDefinition } from "cytoscape";
// @ts-expect-error — cytoscape-fcose ships no types
import fcose from "cytoscape-fcose";
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { GraphMinimap } from "./GraphMinimap";
import { cyStyle } from "./cyStyle";

cytoscape.use(fcose);

type Layer = "service" | "node" | "storage"; // "workload" (the dependency mesh) is always on
type LayoutMode = "force" | "hierarchy";

const REL_LABEL: Record<string, string> = {
  flow: "calls",
  "runs-on": "runs on",
  selects: "fronts",
  mounts: "mounts",
};
// which optional layer an edge belongs to (flow has none — always shown)
const EDGE_LAYER: Record<string, Layer | undefined> = {
  selects: "service",
  "runs-on": "node",
  mounts: "storage",
};
// the mutable state classes reconciled on an in-place poll update (the interaction
// classes — hidden/dim/hl/focus — are deliberately left untouched).
const STATE_CLASSES = ["sev-ok", "sev-info", "sev-degraded", "sev-firing", "secondary", "warned"];

function severityOf(n: TopoNode): Severity {
  if (n.degraded) return "firing";
  if (n.matched) return "degraded";
  if (n.loud) return "info";
  return "ok";
}
function groupId(ns: string) {
  return `ns::${ns || "cluster"}`;
}

function toElements(topo: TopologyView): ElementDefinition[] {
  const els: ElementDefinition[] = [];
  const nodeIds = new Set(topo.nodes.map((n) => n.ceiKey));
  // flow degree → node size (Neo4j makes hubs visibly larger)
  const deg = new Map<string, number>();
  for (const e of topo.edges) {
    if (e.type !== "flow") continue;
    deg.set(e.from, (deg.get(e.from) ?? 0) + 1);
    deg.set(e.to, (deg.get(e.to) ?? 0) + 1);
  }
  const groups = new Set<string>();
  const seenNodeIds = new Set<string>();
  for (const n of topo.nodes) {
    // Defensive: a node id (ceiKey) must be unique — Cytoscape rejects a second
    // element with the same id and the whole render aborts. The backend already
    // guarantees one node per entity, but never let a duplicate from any source
    // (any cluster shape, any future lane) crash the graph.
    if (seenNodeIds.has(n.ceiKey)) continue;
    seenNodeIds.add(n.ceiKey);
    const gid = groupId(n.namespace);
    if (!groups.has(gid)) {
      groups.add(gid);
      els.push({ data: { id: gid, label: n.namespace || "cluster" }, classes: "group" });
    }
    const sev = severityOf(n);
    const layer = n.layer || "workload";
    const secondary = layer === "service" || layer === "storage";
    const base = layer === "service" || layer === "storage" ? 19 : layer === "node" ? 28 : 30;
    const diam = layer === "workload" ? base + Math.min((deg.get(n.ceiKey) ?? 0) * 3.5, 22) : base;
    els.push({
      data: {
        id: n.ceiKey,
        parent: gid,
        label: n.name,
        kind: n.kind,
        namespace: n.namespace,
        layer,
        replicas: n.replicas ?? 0,
        sev,
        diam,
        phenomena: n.phenomena ?? [],
        loud: n.loud,
      },
      classes: cx("entity", secondary ? "secondary" : `sev-${sev}`, n.warned && "warned"),
    });
  }
  for (const e of topo.edges) {
    if (!nodeIds.has(e.from) || !nodeIds.has(e.to)) continue;
    els.push({
      data: {
        id: `${e.from}__${e.to}__${e.type}`,
        source: e.from,
        target: e.to,
        etype: e.type,
        status: e.status,
        rel: REL_LABEL[e.type] ?? e.type,
      },
    });
  }
  return els;
}

const FCOSE = {
  name: "fcose",
  quality: "proof", // small graph (≤400 nodes) — afford the crisper, less-overlapping solve
  animate: false,
  randomize: false,
  nodeSeparation: 135, // was 95 — more air between every pair
  idealEdgeLength: 115, // was 78 — connected nodes sit further apart so edges read cleanly
  nodeRepulsion: 13000, // was 9500 — stronger spread, fewer crossings
  gravity: 0.28, // gentle centering so components don't scatter
  gravityRangeCompound: 1.4,
  packComponents: true,
  tile: true,
  tilingPaddingVertical: 16,
  tilingPaddingHorizontal: 16,
  padding: 54, // was 40 — frame the graph with whitespace
} as const;

// Force = fcose laid out on the dependency mesh (placement/routing never distort
// it). Hierarchy = a layered breadth-first over the call graph (roots = workloads
// nothing calls), which reads a service chain top-to-bottom.
function runLayout(cy: Core, mode: LayoutMode) {
  const vis = cy.elements(":visible");
  let eles = vis;
  let opts: Record<string, unknown> = FCOSE;
  if (mode === "hierarchy") {
    const roots = cy
      .nodes(".entity:visible")
      .filter((n) => n.incomers('edge[etype = "flow"]:visible').length === 0);
    opts = {
      name: "breadthfirst",
      directed: true,
      grid: true,
      spacingFactor: 1.35,
      padding: 54,
      animate: false,
      roots: roots.length ? roots : undefined,
    };
  } else {
    // lay out on the dependency edges only
    eles = vis.difference(cy.edges('[etype != "flow"]'));
  }
  const layout = eles.layout(opts as unknown as cytoscape.LayoutOptions);
  layout.one("layoutstop", () =>
    cy.animate({ fit: { eles: cy.elements(":visible"), padding: 48 }, duration: 250 }),
  );
  layout.run();
}

type SelInfo = {
  id: string;
  label: string;
  kind: string;
  namespace: string;
  layer: string;
  sev: Severity;
  replicas: number;
  phenomena: string[];
  callers: string[];
  callees: string[];
  services: string[];
  runsOn: string[];
  mounts: string[];
};

export function ClusterGraph() {
  const { data: topo, isLoading, error } = useTopology();
  const elRef = useRef<HTMLDivElement>(null);
  const cyRef = useRef<Core | null>(null);
  const [cy, setCy] = useState<Core | null>(null);
  const sigRef = useRef<string>("");
  const [sel, setSel] = useState<SelInfo | null>(null);
  const [isolated, setIsolated] = useState<string | null>(null);

  // filter / view state
  const [q, setQ] = useState("");
  const [nsFilter, setNsFilter] = useState<Set<string>>(new Set());
  const [failuresOnly, setFailuresOnly] = useState(false);
  const [layers, setLayers] = useState<Set<Layer>>(new Set()); // workload always on
  const [layout, setLayout] = useState<LayoutMode>("force");
  // chrome disclosure state — the legend and namespace filter open on demand so the
  // canvas stays the hero (progressive disclosure, never permanent clutter).
  const [legendOpen, setLegendOpen] = useState(false);
  const [nsOpen, setNsOpen] = useState(false);

  const namespaces = useMemo(
    () => Array.from(new Set((topo?.nodes ?? []).map((n) => n.namespace || "cluster"))).sort(),
    [topo],
  );

  function describe(n: cytoscape.NodeSingular): SelInfo {
    const lbls = (c: cytoscape.NodeCollection) =>
      Array.from(new Set(c.map((x) => String(x.data("label"))))).sort();
    return {
      id: n.id(),
      label: n.data("label"),
      kind: n.data("kind"),
      namespace: n.data("namespace"),
      layer: n.data("layer"),
      sev: n.data("sev"),
      replicas: n.data("replicas") ?? 0,
      phenomena: n.data("phenomena") ?? [],
      callers: lbls(n.incomers('edge[etype = "flow"]').sources()),
      callees: lbls(n.outgoers('edge[etype = "flow"]').targets()),
      services: lbls(n.incomers('edge[etype = "selects"]').sources()),
      runsOn: lbls(n.outgoers('edge[etype = "runs-on"]').targets()),
      mounts: lbls(n.outgoers('edge[etype = "mounts"]').targets()),
    };
  }

  // Reveal a node's connections that live in a toggled-off layer (selects / mounts /
  // runs-on) — ghosted — so a node connected ONLY in another layer still shows its links
  // when inspected, instead of reading as orphaned. Only un-hides edges/endpoints that are
  // currently hidden; clearGhost re-hides exactly those.
  function revealCrossLayer(n: cytoscape.NodeSingular) {
    n.connectedEdges().forEach((e) => {
      if (!e.hasClass("hidden")) return;
      e.removeClass("hidden").addClass("ghost");
      const other = e.source().id() === n.id() ? e.target() : e.source();
      if (other.hasClass("hidden")) other.removeClass("hidden").addClass("ghost");
    });
  }
  function clearGhost(c: Core) {
    c.elements(".ghost").addClass("hidden").removeClass("ghost");
  }

  function focusNode(n: cytoscape.NodeSingular) {
    const c = n.cy();
    clearGhost(c);
    revealCrossLayer(n); // pull in cross-layer links (ghosted) BEFORE computing the hood
    c.elements().removeClass("dim hl focus");
    c.elements().addClass("dim");
    const hood = n.closedNeighborhood();
    hood.removeClass("dim");
    hood.nodes().parent().removeClass("dim");
    n.connectedEdges(":visible").removeClass("dim");
    n.connectedEdges(":visible").not(".ghost").addClass("hl"); // real edges highlight; ghost stays faint
    n.removeClass("dim").addClass("focus");
  }
  function clearFocus(c: Core) {
    clearGhost(c);
    c.elements().removeClass("dim hl focus hover");
  }

  // create the cytoscape instance once
  // biome-ignore lint/correctness/useExhaustiveDependencies: init-once; the handlers read live refs/state, never stale closures
  useEffect(() => {
    if (!elRef.current || cyRef.current) return;
    const c = cytoscape({
      container: elRef.current,
      style: cyStyle as unknown as cytoscape.CytoscapeOptions["style"],
      minZoom: 0.12,
      maxZoom: 3.5,
      wheelSensitivity: 0.25,
      pixelRatio: 1,
    });
    cyRef.current = c;
    setCy(c);

    // hover halo + transient highlight (only when nothing is pinned)
    c.on("mouseover", "node.entity", (ev) => {
      ev.target.addClass("hover");
      if (!selRef.current) {
        ev.target.connectedEdges(":visible").addClass("hl");
        ev.target.neighborhood("node").addClass("focus-soft");
      }
    });
    c.on("mouseout", "node.entity", (ev) => {
      ev.target.removeClass("hover");
      if (!selRef.current) {
        c.edges().removeClass("hl");
        c.nodes().removeClass("focus-soft");
      }
    });
    // click → select + pin highlight
    c.on("tap", "node.entity", (ev) => {
      const n = ev.target as cytoscape.NodeSingular;
      setSel(describe(n));
      focusNode(n);
    });
    // double-click → isolate the node's neighborhood
    c.on("dbltap", "node.entity", (ev) => {
      setIsolated(ev.target.id());
    });
    // background tap → clear
    c.on("tap", (ev) => {
      if (ev.target === c) {
        setSel(null);
        clearFocus(c);
      }
    });
    return () => {
      c.destroy();
      cyRef.current = null;
      setCy(null);
    };
  }, []);

  // keep a ref of the current selection for the hover handler (avoids stale closure)
  const selRef = useRef<SelInfo | null>(null);
  useEffect(() => {
    selRef.current = sel;
  }, [sel]);

  // keep cytoscape sized to its container and recover the view on any resize
  // (a real window resize, or the layout having run before the panel had width).
  useEffect(() => {
    const c = cyRef.current;
    const el = elRef.current;
    if (!c || !el) return;
    let t: number | undefined;
    const ro = new ResizeObserver(() => {
      c.resize();
      window.clearTimeout(t);
      t = window.setTimeout(() => c.fit(c.elements(":visible"), 48), 120);
    });
    ro.observe(el);
    return () => {
      ro.disconnect();
      window.clearTimeout(t);
    };
  }, []);

  // sync data. The 15s poll usually returns the SAME node set with only updated
  // marks, so we update state IN PLACE — destroying + re-adding elements would
  // drop their positions (collapsing every node onto one point) and reset the
  // filters/selection. We only rebuild + re-layout on a real structural change.
  useEffect(() => {
    const c = cyRef.current;
    if (!c || !topo) return;
    const els = toElements(topo);
    const sig = els
      .filter(
        (e) =>
          (e.classes as string)?.includes("entity") || (e.classes as string)?.includes("group"),
      )
      .map((e) => e.data.id)
      .sort()
      .join("|");

    if (sig === sigRef.current && c.elements().nonempty()) {
      // same nodes → patch data + state classes; keep positions/filters/selection
      const keep = new Set(els.map((e) => e.data.id as string));
      c.batch(() => {
        for (const e of els) {
          const id = e.data.id as string;
          const ex = c.getElementById(id);
          if (ex.empty()) {
            c.add(e); // a new edge between existing nodes (e.g. an edge turning suspect)
            continue;
          }
          ex.data(e.data);
          if (ex.isNode() && (e.classes as string)?.includes("entity")) {
            ex.removeClass(STATE_CLASSES.join(" "));
            const next = (e.classes as string).split(" ").filter((x) => STATE_CLASSES.includes(x));
            if (next.length) ex.addClass(next.join(" "));
          }
        }
        // drop edges that retracted since the last poll
        for (const ed of c.edges()) {
          if (!keep.has(ed.id())) ed.remove();
        }
      });
      return;
    }

    // structural change → rebuild + re-layout
    sigRef.current = sig;
    c.batch(() => {
      c.elements().remove();
      c.add(els);
    });
    runLayout(c, layout);
    setSel(null);
    setIsolated(null);
  }, [topo, layout]);

  // apply filters + layer toggles + isolation (toggle .hidden — no relayout)
  // biome-ignore lint/correctness/useExhaustiveDependencies: re-run when topo updates too
  useEffect(() => {
    const c = cyRef.current;
    if (!c) return;
    const ql = q.trim().toLowerCase();
    const iso = isolated ? c.getElementById(isolated) : null;
    const keep = iso?.nonempty() ? iso.closedNeighborhood().nodes() : null;
    c.batch(() => {
      c.nodes(".entity").forEach((n) => {
        const layer = (n.data("layer") as string) || "workload";
        const ns = n.data("namespace") || "cluster";
        const sev = n.data("sev") as Severity;
        let show = true;
        if (layer !== "workload" && !layers.has(layer as Layer)) show = false;
        if (nsFilter.size > 0 && !nsFilter.has(ns)) show = false;
        if (failuresOnly && sev !== "firing" && sev !== "degraded" && !n.data("loud")) show = false;
        if (ql && !String(n.data("label")).toLowerCase().includes(ql)) show = false;
        if (keep && !keep.contains(n)) show = false;
        n.toggleClass("hidden", !show);
      });
      c.edges().forEach((e) => {
        const src = e.source() as cytoscape.NodeSingular;
        const tgt = e.target() as cytoscape.NodeSingular;
        const lyr = EDGE_LAYER[e.data("etype") as string];
        const hidden =
          src.hasClass("hidden") || tgt.hasClass("hidden") || (lyr != null && !layers.has(lyr));
        e.toggleClass("hidden", hidden);
      });
      // reveal-on-select must survive a poll re-run (this effect re-runs on topo): the loops
      // above re-hid any prior ghost, so drop the stale class and re-reveal the selected
      // node's cross-layer links before group visibility is computed (so a pulled-in
      // Service/PVC keeps its namespace group visible).
      c.elements(".ghost").removeClass("ghost");
      const selNode = selRef.current ? c.getElementById(selRef.current.id) : null;
      if (selNode?.nonempty()) revealCrossLayer(selNode as cytoscape.NodeSingular);
      c.nodes(".group").forEach((g) => {
        const anyVisible = g
          .children(".entity")
          .some((ch) => !(ch as cytoscape.NodeSingular).hasClass("hidden"));
        g.toggleClass("hidden", !anyVisible);
      });
      // "connected elsewhere" badge: a visible node that HAS edges but none visible in this
      // view is connected only in a toggled-off layer — mark it (a dashed ring) so "no edge
      // here" never reads as "orphaned". The selected node, now showing ghost edges, drops it.
      c.nodes(".entity").forEach((n) => {
        const connected = n.connectedEdges().length > 0;
        const anyVisible = n.connectedEdges(":visible").length > 0;
        n.toggleClass("cross-layer", !n.hasClass("hidden") && connected && !anyVisible);
      });
    });
  }, [q, nsFilter, failuresOnly, layers, isolated, topo, sel]);

  const counts = useMemo(() => {
    const s = { firing: 0, matched: 0, loud: 0, workloads: 0, services: 0, total: 0 };
    for (const n of topo?.nodes ?? []) {
      const layer = n.layer || "workload";
      if (layer === "workload") s.workloads++;
      if (layer === "service") s.services++;
      s.total++;
      if (n.degraded) s.firing++;
      else if (n.matched) s.matched++;
      if (n.loud) s.loud++;
    }
    return s;
  }, [topo]);

  function zoomBy(f: number) {
    if (!cy) return;
    cy.animate({
      zoom: { level: cy.zoom() * f, position: { x: cy.width() / 2, y: cy.height() / 2 } },
      duration: 140,
    });
  }
  function fit() {
    cy?.animate({ fit: { eles: cy.elements(":visible"), padding: 40 }, duration: 250 });
  }
  function toggleLayer(l: Layer) {
    setLayers((p) => {
      const n = new Set(p);
      if (n.has(l)) n.delete(l);
      else n.add(l);
      return n;
    });
  }
  function toggleNs(ns: string) {
    setNsFilter((p) => {
      const n = new Set(p);
      if (n.has(ns)) n.delete(ns);
      else n.add(ns);
      return n;
    });
  }

  return (
    <div className="relative flex h-full min-h-0 flex-col">
      {/* ── Top chrome: identity · live state · find · layers · view actions ── */}
      <header className="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-2.5 border-b border-rule px-5 py-3">
        {/* identity + hero count + live severity (the floating stats chip, folded in) */}
        <div className="flex min-w-0 items-center gap-3.5">
          <div className="flex flex-col leading-none">
            <span className="v-eyebrow text-[9px]">Cluster graph</span>
            <div className="mt-1.5 flex items-baseline gap-1.5">
              <span className="font-display text-[20px] font-semibold tracking-tight text-ink">
                {counts.workloads}
              </span>
              <span className="text-[11.5px] text-ink-low">
                workload{counts.workloads === 1 ? "" : "s"}
                {layers.has("service") && counts.services > 0 ? ` · ${counts.services} svc` : ""}
              </span>
            </div>
          </div>
          {(counts.firing > 0 || counts.matched > 0 || counts.loud > 0) && (
            <div className="flex items-center gap-3 border-l border-rule pl-3.5">
              {counts.firing > 0 && <ChromeStat sev="firing" n={counts.firing} label="firing" />}
              {counts.matched > 0 && (
                <ChromeStat sev="degraded" n={counts.matched} label="matched" />
              )}
              {counts.loud > 0 && <ChromeStat sev="info" n={counts.loud} label="loud" />}
            </div>
          )}
          {(topo?.truncated ?? 0) > 0 && (
            <span
              className="self-center rounded-[4px] border border-rule px-1.5 py-0.5 font-mono text-[10px] text-ink-low"
              title="entities omitted past the 400-node cap"
            >
              +{topo?.truncated} hidden
            </span>
          )}
        </div>

        {/* find + layers — the primary controls */}
        <div className="flex flex-1 flex-wrap items-center gap-2">
          <label className="flex items-center gap-2 rounded-[6px] border border-rule bg-surface px-2.5 py-1.5 transition-colors focus-within:border-rule-strong">
            <Icon.search size={13} className="text-ink-low" />
            <input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="Find a node…"
              className="w-28 bg-transparent text-[12.5px] text-ink outline-none placeholder:text-ink-low"
            />
            {q && (
              <button
                type="button"
                onClick={() => setQ("")}
                className="cursor-pointer text-ink-low hover:text-ink"
                title="Clear"
              >
                <Icon.close size={12} />
              </button>
            )}
          </label>

          {/* layers — a segmented control; the dependency mesh is always on */}
          <div className="flex items-center gap-0.5 rounded-[6px] border border-rule bg-surface p-0.5">
            <span className="px-1.5 font-mono text-[8.5px] tracking-[0.1em] text-ink-low uppercase">
              layers
            </span>
            <Seg active title="Service-to-service dependencies — always on">
              Deps
            </Seg>
            <Seg
              active={layers.has("service")}
              onClick={() => toggleLayer("service")}
              title="k8s Services → workload (routing)"
            >
              Routing
            </Seg>
            <Seg
              active={layers.has("node")}
              onClick={() => toggleLayer("node")}
              title="Nodes + placement (runs-on)"
            >
              Infra
            </Seg>
            <Seg
              active={layers.has("storage")}
              onClick={() => toggleLayer("storage")}
              title="PVCs + mounts"
            >
              Storage
            </Seg>
          </div>
        </div>

        {/* view actions */}
        <div className="flex items-center gap-1.5">
          <Button
            variant="secondary"
            active={failuresOnly}
            onClick={() => setFailuresOnly((v) => !v)}
            title="Show only firing / loud nodes"
          >
            <Icon.filter size={13} /> Failures
          </Button>
          <Button
            variant="secondary"
            active={layout === "hierarchy"}
            onClick={() => setLayout((m) => (m === "force" ? "hierarchy" : "force"))}
            title="Toggle force-directed / layered layout"
          >
            {layout === "force" ? "Force" : "Layered"}
          </Button>

          {/* namespaces — a popover, so it scales past a handful of namespaces */}
          <div className="relative">
            <Button
              variant="secondary"
              active={nsFilter.size > 0}
              onClick={() => setNsOpen((v) => !v)}
              title="Filter by namespace"
            >
              Namespaces{nsFilter.size > 0 ? ` · ${nsFilter.size}` : ""} <Icon.chevron size={11} />
            </Button>
            {nsOpen && (
              <>
                <button
                  type="button"
                  aria-label="Close namespace filter"
                  className="fixed inset-0 z-30 cursor-default"
                  onClick={() => setNsOpen(false)}
                />
                <div className="v-enter absolute right-0 top-[calc(100%+6px)] z-40 w-56 rounded-[8px] border border-rule-strong bg-surface p-1.5 shadow-xl">
                  <div className="flex items-center justify-between px-1.5 pb-1.5">
                    <span className="v-eyebrow">Namespaces</span>
                    {nsFilter.size > 0 && (
                      <button
                        type="button"
                        onClick={() => setNsFilter(new Set())}
                        className="cursor-pointer text-[10.5px] text-ink-low hover:text-ink"
                      >
                        clear
                      </button>
                    )}
                  </div>
                  <div className="max-h-64 overflow-y-auto pr-0.5">
                    {namespaces.map((ns) => {
                      const shown = nsFilter.size === 0 || nsFilter.has(ns);
                      return (
                        <button
                          key={ns}
                          type="button"
                          onClick={() => toggleNs(ns)}
                          className={cx(
                            "flex w-full cursor-pointer items-center gap-2 rounded-[5px] px-1.5 py-1 text-left font-mono text-[11px] transition-colors",
                            nsFilter.has(ns)
                              ? "bg-surface-hi text-ink"
                              : "text-ink-mid hover:bg-surface-hi",
                          )}
                        >
                          <span
                            className={cx(
                              "inline-block size-1.5 rounded-full",
                              shown ? "bg-ink-mid" : "bg-mute",
                            )}
                          />
                          <span className="truncate">{ns}</span>
                        </button>
                      );
                    })}
                  </div>
                </div>
              </>
            )}
          </div>

          {/* zoom + layout cluster — a single unified pill */}
          <div className="flex items-center overflow-hidden rounded-[6px] border border-rule bg-surface">
            {isolated && (
              <IconBtn onClick={() => setIsolated(null)} title="Clear isolation" divider>
                <Icon.close size={12} />
              </IconBtn>
            )}
            <IconBtn onClick={() => zoomBy(1.3)} title="Zoom in">
              +
            </IconBtn>
            <IconBtn onClick={fit} title="Fit to view" divider wide>
              Fit
            </IconBtn>
            <IconBtn onClick={() => zoomBy(1 / 1.3)} title="Zoom out">
              −
            </IconBtn>
            <IconBtn onClick={() => cy && runLayout(cy, layout)} title="Re-run layout" divider>
              <Icon.cluster size={13} />
            </IconBtn>
          </div>
        </div>
      </header>

      {/* canvas */}
      <div className="relative min-h-0 flex-1">
        <div ref={elRef} className="h-full w-full v-grid-faint" />

        {(isLoading || !topo) && !error && (
          <div className="pointer-events-none absolute inset-0 grid place-items-center">
            <span className="flex items-center gap-2 text-[12px] text-ink-mid">
              <Spinner /> loading topology…
            </span>
          </div>
        )}
        {error && (
          <div className="absolute inset-0 grid place-items-center">
            <div className="v-panel-inset max-w-sm p-4 text-center text-[12.5px] text-ink-mid">
              Topology unavailable. Is <Mono>obsd</Mono> running on <Mono>:9095</Mono>?
            </div>
          </div>
        )}

        <GraphMinimap cy={cy} />

        {/* legend — collapsed to a pill, opens a popover (the stats now live in the chrome) */}
        <LegendControl open={legendOpen} setOpen={setLegendOpen} />

        {sel && (
          <NodeDetail
            sel={sel}
            isolated={isolated === sel.id}
            onIsolate={() => setIsolated((v) => (v === sel.id ? null : sel.id))}
            onClose={() => {
              setSel(null);
              if (cyRef.current) clearFocus(cyRef.current);
            }}
          />
        )}
      </div>
    </div>
  );
}

// A live severity count in the top chrome (replaces the floating stats chip).
function ChromeStat({ sev, n, label }: { sev: Severity; n: number; label: string }) {
  return (
    <span className="flex items-center gap-1.5 leading-none" title={`${n} ${label}`}>
      <StateDot sev={sev} size={7} />
      <span className="font-mono text-[12px] text-ink">{n}</span>
      <span className="text-[10.5px] text-ink-low">{label}</span>
    </span>
  );
}

// One segment of the Layers control.
function Seg({
  active,
  onClick,
  title,
  children,
}: { active?: boolean; onClick?: () => void; title?: string; children: ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={title}
      disabled={!onClick}
      className={cx(
        "rounded-[4px] px-2 py-1 text-[11.5px] leading-none transition-colors",
        onClick && "cursor-pointer",
        active ? "bg-surface-hi text-ink" : "text-ink-low hover:text-ink-mid",
      )}
    >
      {children}
    </button>
  );
}

// One control inside the unified zoom/layout pill.
function IconBtn({
  onClick,
  title,
  children,
  wide,
  divider,
}: {
  onClick: () => void;
  title?: string;
  children: ReactNode;
  wide?: boolean;
  divider?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={title}
      className={cx(
        "flex h-7 cursor-pointer items-center justify-center text-[12px] text-ink-low transition-colors hover:bg-surface-hi hover:text-ink",
        wide ? "px-2.5" : "w-7",
        divider && "border-l border-rule",
      )}
    >
      {children}
    </button>
  );
}

// The legend — collapsed to a pill by default; opens a clean popover with the State +
// Kind key AND an explanation of the cross-layer (dashed-ring) affordance so it never
// reads as "broken".
function LegendControl({ open, setOpen }: { open: boolean; setOpen: (v: boolean) => void }) {
  return (
    <div className="absolute bottom-3 left-3 z-20 flex flex-col items-start">
      {open && (
        <div className="v-enter mb-1.5 w-60 rounded-[10px] border border-rule bg-surface/95 p-3.5 shadow-xl backdrop-blur">
          <div className="v-eyebrow mb-2">State</div>
          <div className="flex flex-col gap-1.5">
            <LegendRow sev="firing" label="Firing / degraded" />
            <LegendRow sev="degraded" label="Matched phenomenon" />
            <LegendRow sev="info" label="Unexplained (loud)" />
            <LegendRow sev="ok" label="Healthy" />
            <div className="flex items-center gap-2 text-[10.5px] text-ink-mid">
              <span
                className="inline-block size-2.5 rounded-full border border-dashed"
                style={{ borderColor: signal.info }}
              />
              warned — projected “might”
            </div>
          </div>
          <div className="v-eyebrow mt-3.5 mb-2">Kind</div>
          <div className="grid grid-cols-2 gap-x-3 gap-y-1.5 text-[10px] text-ink-mid">
            <ShapeRow shape="circle" label="Workload" />
            <ShapeRow shape="square" label="Node" />
            <ShapeRow shape="hex" label="DaemonSet" />
            <ShapeRow shape="diamond" label="StaticPod" />
            <ShapeRow shape="tag" label="Service" />
            <ShapeRow shape="barrel" label="PVC" />
          </div>
          <div className="mt-3.5 flex gap-2 border-t border-rule pt-2.5 text-[10px] leading-relaxed text-ink-low">
            <span className="mt-[3px] inline-block size-2 shrink-0 rounded-full border border-dashed border-ink-low" />
            <span>
              Dashed ring = connected only in a hidden layer. Select it, or toggle Routing / Infra /
              Storage, to reveal those links.
            </span>
          </div>
        </div>
      )}
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className={cx(
          "flex cursor-pointer items-center gap-1.5 rounded-[6px] border px-2.5 py-1.5 text-[11px] backdrop-blur transition-colors",
          open
            ? "border-rule-strong bg-surface text-ink"
            : "border-rule bg-plane/70 text-ink-low hover:text-ink-mid",
        )}
      >
        Legend
        <Icon.chevron size={11} className={cx("transition-transform", open && "rotate-180")} />
      </button>
    </div>
  );
}

function LegendRow({ sev, label }: { sev: Severity; label: string }) {
  return (
    <div className="flex items-center gap-2 text-[10.5px] text-ink-mid">
      <StateDot sev={sev} size={9} />
      {label}
    </div>
  );
}

function ShapeRow({ shape, label }: { shape: string; label: string }) {
  const s: Record<string, string> = {
    circle: "rounded-full",
    square: "rounded-[2px]",
    hex: "rounded-[2px] rotate-12",
    diamond: "rotate-45 rounded-[1px]",
    tag: "rounded-[1px]",
    barrel: "rounded-[3px]",
  };
  return (
    <div className="flex items-center gap-1.5">
      <span
        className={cx("inline-block size-2 border", s[shape])}
        style={{ borderColor: plane.inkLow }}
      />
      {label}
    </div>
  );
}

function NodeDetail({
  sel,
  isolated,
  onIsolate,
  onClose,
}: {
  sel: SelInfo;
  isolated: boolean;
  onIsolate: () => void;
  onClose: () => void;
}) {
  const kindLabel =
    sel.layer === "service"
      ? "Service"
      : sel.layer === "storage"
        ? "PVC"
        : sel.layer === "node"
          ? "Node"
          : sel.kind;
  const secondary = sel.layer === "service" || sel.layer === "storage";
  return (
    <div className="v-enter absolute top-3 right-3 z-30 flex max-h-[calc(100%-1.5rem)] w-[304px] max-w-[calc(100%-1.5rem)] flex-col overflow-hidden rounded-[12px] border border-rule-strong bg-surface shadow-xl">
      {/* header — sticky identity */}
      <div className="flex shrink-0 items-start justify-between gap-2 border-b border-rule px-4 py-3.5">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            {secondary ? (
              <span className="inline-block size-2.5 rounded-[2px] border border-mute" />
            ) : (
              <StateDot sev={sel.sev} size={9} />
            )}
            <span className="truncate font-display text-[15px] font-semibold text-ink">
              {sel.label}
            </span>
          </div>
          <Mono className="mt-1 block text-[11px] text-ink-low">
            {sel.namespace || "cluster"} · {kindLabel}
            {sel.replicas > 0 ? ` · ${sel.replicas} pod${sel.replicas > 1 ? "s" : ""}` : ""}
          </Mono>
        </div>
        <button
          type="button"
          onClick={onClose}
          title="Close"
          className="-mr-1 cursor-pointer text-ink-low transition-colors hover:text-ink"
        >
          <Icon.close size={15} />
        </button>
      </div>

      {/* body — scrolls */}
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3.5">
        {sel.phenomena.length > 0 && (
          <div className="mb-4">
            <div className="v-eyebrow mb-1.5">Phenomena</div>
            <div className="flex flex-col gap-1">
              {sel.phenomena.map((p) => (
                <Mono
                  key={p}
                  className="rounded-[5px] bg-surface-hi px-2 py-1 text-[11px] text-ink-soft"
                >
                  {p}
                </Mono>
              ))}
            </div>
          </div>
        )}

        <div className="grid grid-cols-2 gap-x-4 gap-y-4">
          <RelList title="Upstream" hint="calls this" items={sel.callers} />
          <RelList title="Downstream" hint="this calls" items={sel.callees} />
        </div>
        {(sel.services.length > 0 || sel.runsOn.length > 0 || sel.mounts.length > 0) && (
          <div className="mt-4 grid grid-cols-2 gap-x-4 gap-y-4 border-t border-rule pt-4">
            {sel.services.length > 0 && (
              <RelList title="Fronted by" hint="Services routing to it" items={sel.services} />
            )}
            {sel.runsOn.length > 0 && (
              <RelList title="Runs on" hint="placement" items={sel.runsOn} />
            )}
            {sel.mounts.length > 0 && <RelList title="Mounts" hint="storage" items={sel.mounts} />}
          </div>
        )}
      </div>

      {/* footer action */}
      <div className="shrink-0 border-t border-rule p-3">
        <button
          type="button"
          onClick={onIsolate}
          className="w-full cursor-pointer rounded-[6px] border border-rule-strong bg-surface-hi px-2 py-1.5 text-[11.5px] text-ink-mid transition-colors hover:text-ink"
        >
          {isolated ? "Show full graph" : "Isolate neighborhood"}
        </button>
      </div>
    </div>
  );
}

function RelList({ title, hint, items }: { title: string; hint: string; items: string[] }) {
  return (
    <div>
      <div className="v-eyebrow mb-1.5" title={hint}>
        {title} · {items.length}
      </div>
      <div className="flex flex-col gap-1">
        {items.length === 0 ? (
          <span className="text-[11px] text-ink-low">—</span>
        ) : (
          items.map((s) => (
            <span key={s} className="truncate text-[11.5px] text-ink-mid" title={s}>
              {s}
            </span>
          ))
        )}
      </div>
    </div>
  );
}
