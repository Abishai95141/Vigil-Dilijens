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
import { useEffect, useMemo, useRef, useState } from "react";
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
  for (const n of topo.nodes) {
    const gid = groupId(n.namespace);
    if (!groups.has(gid)) {
      groups.add(gid);
      els.push({ data: { id: gid, label: n.namespace || "cluster" }, classes: "group" });
    }
    const sev = severityOf(n);
    const layer = n.layer || "workload";
    const secondary = layer === "service" || layer === "storage";
    const base = layer === "service" || layer === "storage" ? 17 : layer === "node" ? 24 : 26;
    const diam = layer === "workload" ? base + Math.min((deg.get(n.ceiKey) ?? 0) * 3, 18) : base;
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
  quality: "default",
  animate: false,
  randomize: false,
  nodeSeparation: 95,
  idealEdgeLength: 78,
  nodeRepulsion: 9500,
  packComponents: true,
  tile: true,
  padding: 40,
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
      spacingFactor: 1.15,
      padding: 40,
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

  function focusNode(n: cytoscape.NodeSingular) {
    const c = n.cy();
    c.elements().removeClass("dim hl focus");
    c.elements().addClass("dim");
    const hood = n.closedNeighborhood();
    hood.removeClass("dim");
    hood.nodes().parent().removeClass("dim");
    n.connectedEdges(":visible").removeClass("dim").addClass("hl");
    n.removeClass("dim").addClass("focus");
  }
  function clearFocus(c: Core) {
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

  // sync data (re-layout only when the node set changes)
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
    c.batch(() => {
      c.elements().remove();
      c.add(els);
    });
    if (sig !== sigRef.current) {
      sigRef.current = sig;
      runLayout(c, layout);
    }
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
      c.nodes(".group").forEach((g) => {
        const anyVisible = g
          .children(".entity")
          .some((ch) => !(ch as cytoscape.NodeSingular).hasClass("hidden"));
        g.toggleClass("hidden", !anyVisible);
      });
    });
  }, [q, nsFilter, failuresOnly, layers, isolated, topo]);

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
      {/* controls bar */}
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-rule px-4 py-2.5">
        <div className="flex items-center gap-2 rounded-[6px] border border-rule bg-surface px-2.5 py-1.5">
          <span className="text-ink-low">
            <Icon.search size={14} />
          </span>
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search nodes…"
            className="w-36 bg-transparent text-[12.5px] text-ink outline-none placeholder:text-ink-low"
          />
        </div>

        {/* layer toggles — the dependency mesh is always on */}
        <div className="flex items-center gap-1 rounded-[6px] border border-rule bg-surface px-1 py-1">
          <span className="px-1.5 font-mono text-[9.5px] tracking-[0.08em] text-ink-low uppercase">
            layers
          </span>
          <Chip on label="Deps" title="Service-to-service dependencies (always on)" />
          <Chip
            on={layers.has("service")}
            onClick={() => toggleLayer("service")}
            label="Routing"
            title="k8s Services → workload (pod-to-service)"
          />
          <Chip
            on={layers.has("node")}
            onClick={() => toggleLayer("node")}
            label="Infra"
            title="Nodes + placement (runs-on)"
          />
          <Chip
            on={layers.has("storage")}
            onClick={() => toggleLayer("storage")}
            label="Storage"
            title="PVCs + mounts"
          />
        </div>

        <Button
          variant="secondary"
          active={failuresOnly}
          onClick={() => setFailuresOnly((v) => !v)}
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

        <div className="ml-1 flex flex-wrap items-center gap-1">
          {namespaces.map((ns) => (
            <button
              key={ns}
              type="button"
              onClick={() => toggleNs(ns)}
              className={cx(
                "cursor-pointer rounded-[5px] border px-2 py-1 font-mono text-[10px] tracking-[0.04em] transition-colors duration-150",
                nsFilter.size === 0 || nsFilter.has(ns)
                  ? "border-rule-strong bg-surface-hi text-ink-mid hover:text-ink"
                  : "border-rule text-ink-low hover:text-ink-mid",
              )}
            >
              {ns}
            </button>
          ))}
        </div>

        <div className="ml-auto flex items-center gap-1">
          {isolated && (
            <Button variant="ghost" onClick={() => setIsolated(null)} title="Clear isolation">
              <Icon.close size={13} /> Unisolate
            </Button>
          )}
          <Button variant="ghost" onClick={() => zoomBy(1.3)} title="Zoom in">
            +
          </Button>
          <Button variant="ghost" onClick={() => zoomBy(1 / 1.3)} title="Zoom out">
            –
          </Button>
          <Button variant="ghost" onClick={fit} title="Fit to view">
            Fit
          </Button>
          <Button variant="ghost" onClick={() => cy && runLayout(cy, layout)} title="Re-layout">
            <Icon.cluster size={14} />
          </Button>
        </div>
      </div>

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

        {/* legend */}
        <div className="absolute bottom-3 left-3 flex flex-col gap-1.5 rounded-[8px] border border-rule bg-plane/80 px-3 py-2.5 backdrop-blur">
          <div className="v-eyebrow mb-0.5">State</div>
          <LegendRow sev="firing" label="Firing / degraded" />
          <LegendRow sev="degraded" label="Matched phenomenon" />
          <LegendRow sev="info" label="Unexplained (loud)" />
          <LegendRow sev="ok" label="Healthy" />
          <div className="mt-1 flex items-center gap-2 text-[10.5px] text-ink-low">
            <span
              className="inline-block size-2.5 rounded-full border border-dashed"
              style={{ borderColor: signal.info }}
            />
            warned — projected “might”
          </div>
          <div className="mt-1.5 v-eyebrow mb-0.5">Kind</div>
          <div className="grid grid-cols-2 gap-x-3 gap-y-1 text-[10px] text-ink-low">
            <ShapeRow shape="circle" label="Workload" />
            <ShapeRow shape="square" label="Node" />
            <ShapeRow shape="hex" label="DaemonSet" />
            <ShapeRow shape="diamond" label="StaticPod" />
            <ShapeRow shape="tag" label="Service" />
            <ShapeRow shape="barrel" label="PVC" />
          </div>
        </div>

        {/* totals */}
        <div className="absolute top-3 left-3 flex items-center gap-3 rounded-[8px] border border-rule bg-plane/80 px-3 py-2 font-mono text-[11px] backdrop-blur">
          <span className="text-ink-low">
            {counts.workloads} workloads
            {layers.has("service") ? ` · ${counts.services} services` : ""}
          </span>
          {counts.firing > 0 && <span style={{ color: signal.error }}>{counts.firing} firing</span>}
          {counts.matched > 0 && (
            <span style={{ color: signal.warning }}>{counts.matched} matched</span>
          )}
          {counts.loud > 0 && <span style={{ color: signal.info }}>{counts.loud} loud</span>}
          {(topo?.truncated ?? 0) > 0 && (
            <span className="text-ink-low" title="entities omitted past the 400-node cap">
              +{topo?.truncated} hidden
            </span>
          )}
        </div>

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

function Chip({
  on,
  onClick,
  label,
  title,
}: {
  on?: boolean;
  onClick?: () => void;
  label: string;
  title?: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={title}
      disabled={!onClick}
      className={cx(
        "rounded-[4px] px-2 py-0.5 text-[11px] transition-colors",
        onClick && "cursor-pointer",
        on ? "bg-surface-hi text-ink" : "text-ink-low hover:text-ink-mid",
      )}
    >
      {label}
    </button>
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
  return (
    <div className="absolute top-3 right-3 z-20 max-h-[calc(100%-1.5rem)] w-72 overflow-y-auto rounded-[10px] border border-rule-strong bg-surface p-4 shadow-xl">
      <div className="mb-3 flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            {sel.layer === "service" || sel.layer === "storage" ? (
              <span className="inline-block size-2.5 rounded-[2px] border border-mute" />
            ) : (
              <StateDot sev={sel.sev} size={9} />
            )}
            <span className="truncate font-display text-[15px] font-semibold text-ink">
              {sel.label}
            </span>
          </div>
          <Mono className="text-[11px] text-ink-low">
            {sel.namespace || "cluster"} · {kindLabel}
            {sel.replicas > 0 ? ` · ${sel.replicas} pod${sel.replicas > 1 ? "s" : ""}` : ""}
          </Mono>
        </div>
        <button
          type="button"
          onClick={onClose}
          className="cursor-pointer text-ink-low hover:text-ink"
        >
          <Icon.close size={15} />
        </button>
      </div>

      {sel.phenomena.length > 0 && (
        <div className="mb-3">
          <div className="v-eyebrow mb-1.5">Phenomena</div>
          <div className="flex flex-col gap-1">
            {sel.phenomena.map((p) => (
              <Mono
                key={p}
                className="rounded-[4px] bg-surface-hi px-2 py-1 text-[11px] text-ink-soft"
              >
                {p}
              </Mono>
            ))}
          </div>
        </div>
      )}

      <div className="grid grid-cols-2 gap-3">
        <RelList title="Upstream" hint="calls this" items={sel.callers} />
        <RelList title="Downstream" hint="this calls" items={sel.callees} />
      </div>
      {(sel.services.length > 0 || sel.runsOn.length > 0 || sel.mounts.length > 0) && (
        <div className="mt-3 grid grid-cols-2 gap-3 border-t border-rule pt-3">
          {sel.services.length > 0 && (
            <RelList title="Fronted by" hint="Services routing to it" items={sel.services} />
          )}
          {sel.runsOn.length > 0 && <RelList title="Runs on" hint="placement" items={sel.runsOn} />}
          {sel.mounts.length > 0 && <RelList title="Mounts" hint="storage" items={sel.mounts} />}
        </div>
      )}

      <button
        type="button"
        onClick={onIsolate}
        className="mt-3 w-full cursor-pointer rounded-[6px] border border-rule-strong bg-surface-hi px-2 py-1.5 text-[11.5px] text-ink-mid transition-colors hover:text-ink"
      >
        {isolated ? "Show full graph" : "Isolate neighborhood"}
      </button>
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
