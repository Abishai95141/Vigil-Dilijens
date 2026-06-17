import { useTopology } from "@/api/client";
import type { TopoNode, TopologyView } from "@/api/types";
import { Icon } from "@/components/ui/icons";
import { Button, Mono, Spinner, StateDot, cx } from "@/components/ui/primitives";
import { type Severity, signal } from "@/lib/tokens";
import cytoscape, { type Core, type ElementDefinition } from "cytoscape";
// @ts-expect-error — cytoscape-fcose ships no types
import fcose from "cytoscape-fcose";
import { useEffect, useMemo, useRef, useState } from "react";
import { cyStyle } from "./cyStyle";

cytoscape.use(fcose);

function severityOf(n: TopoNode): Severity {
  if (n.degraded) return "firing";
  if (n.matched) return "degraded";
  if (n.loud) return "info";
  return "ok";
}
function groupId(ns: string): string {
  return `ns::${ns || "cluster"}`;
}
function groupLabel(ns: string): string {
  return ns || "cluster nodes";
}

// TopologyView → cytoscape elements (namespace compound groups + entities + edges).
function toElements(topo: TopologyView): ElementDefinition[] {
  const els: ElementDefinition[] = [];
  const seenGroups = new Set<string>();
  const nodeIds = new Set(topo.nodes.map((n) => n.ceiKey));
  for (const n of topo.nodes) {
    const gid = groupId(n.namespace);
    if (!seenGroups.has(gid)) {
      seenGroups.add(gid);
      els.push({ data: { id: gid, label: groupLabel(n.namespace) }, classes: "group" });
    }
    const sev = severityOf(n);
    els.push({
      data: {
        id: n.ceiKey,
        parent: gid,
        label: n.name,
        kind: n.kind,
        namespace: n.namespace,
        sev,
        phenomena: n.phenomena ?? [],
        matched: n.matched,
        loud: n.loud,
        warned: n.warned,
      },
      classes: cx("entity", `sev-${sev}`, n.warned && "warned"),
    });
  }
  for (const e of topo.edges) {
    if (!nodeIds.has(e.from) || !nodeIds.has(e.to)) continue;
    const etype = e.type === "flow" ? "flow" : "struct";
    els.push({
      data: {
        id: `${e.from}__${e.to}__${e.type}`,
        source: e.from,
        target: e.to,
        etype,
        status: e.status,
      },
    });
  }
  return els;
}

type SelInfo = {
  id: string;
  label: string;
  kind: string;
  namespace: string;
  sev: Severity;
  phenomena: string[];
};

const FCOSE = {
  name: "fcose",
  quality: "default",
  animate: false,
  randomize: false,
  nodeSeparation: 90,
  idealEdgeLength: 70,
  nodeRepulsion: 9000,
  packComponents: true,
  tile: true,
  padding: 36,
} as const;

export function ClusterGraph() {
  const { data: topo, isLoading, error } = useTopology();
  const elRef = useRef<HTMLDivElement>(null);
  const cyRef = useRef<Core | null>(null);
  const sigRef = useRef<string>("");
  const [sel, setSel] = useState<SelInfo | null>(null);

  // filter state
  const [q, setQ] = useState("");
  const [nsFilter, setNsFilter] = useState<Set<string>>(new Set());
  const [failuresOnly, setFailuresOnly] = useState(false);
  const [hideStruct, setHideStruct] = useState(false);

  const namespaces = useMemo(
    () => Array.from(new Set((topo?.nodes ?? []).map((n) => n.namespace || "cluster"))).sort(),
    [topo],
  );

  // create the cytoscape instance once
  useEffect(() => {
    if (!elRef.current || cyRef.current) return;
    const cy = cytoscape({
      container: elRef.current,
      style: cyStyle as any,
      minZoom: 0.2,
      maxZoom: 3,
      wheelSensitivity: 0.25,
      pixelRatio: 1,
    });
    cyRef.current = cy;
    cy.on("tap", "node.entity", (ev) => {
      const n = ev.target;
      setSel({
        id: n.id(),
        label: n.data("label"),
        kind: n.data("kind"),
        namespace: n.data("namespace"),
        sev: n.data("sev"),
        phenomena: n.data("phenomena") ?? [],
      });
      cy.elements().addClass("dim");
      const hood = n.closedNeighborhood();
      hood.removeClass("dim");
      hood.nodes().parent().removeClass("dim");
      n.connectedEdges().removeClass("dim").addClass("hl");
      n.addClass("focus");
    });
    cy.on("tap", (ev) => {
      if (ev.target === cy) {
        setSel(null);
        cy.elements().removeClass("dim hl focus");
      }
    });
    return () => {
      cy.destroy();
      cyRef.current = null;
    };
  }, []);

  // sync data (re-layout only when the node set changes)
  useEffect(() => {
    const cy = cyRef.current;
    if (!cy || !topo) return;
    const els = toElements(topo);
    const sig = els
      .filter(
        (e) =>
          (e.classes as string)?.includes("entity") || (e.classes as string)?.includes("group"),
      )
      .map((e) => e.data.id)
      .sort()
      .join("|");
    cy.batch(() => {
      cy.elements().remove();
      cy.add(els);
    });
    if (sig !== sigRef.current) {
      sigRef.current = sig;
      const layout = cy.layout(FCOSE as any);
      layout.one("layoutstop", () =>
        cy.animate({ fit: { eles: cy.elements(), padding: 50 }, duration: 250 }),
      );
      layout.run();
    }
    setSel(null);
  }, [topo]);

  // apply filters (toggle .hidden — no relayout)
  // biome-ignore lint/correctness/useExhaustiveDependencies: re-run when topo updates too
  useEffect(() => {
    const cy = cyRef.current;
    if (!cy) return;
    const ql = q.trim().toLowerCase();
    cy.batch(() => {
      cy.nodes(".entity").forEach((n) => {
        const ns = n.data("namespace") || "cluster";
        const sev = n.data("sev") as Severity;
        let show = true;
        if (nsFilter.size > 0 && !nsFilter.has(ns)) show = false;
        if (failuresOnly && sev !== "firing" && sev !== "degraded" && !n.data("loud")) show = false;
        if (ql && !String(n.data("label")).toLowerCase().includes(ql)) show = false;
        n.toggleClass("hidden", !show);
      });
      cy.edges().forEach((e) => {
        const src = e.source() as any;
        const tgt = e.target() as any;
        const hidden = src.hasClass("hidden") || tgt.hasClass("hidden");
        e.toggleClass("hidden", hidden || (hideStruct && e.data("etype") !== "flow"));
      });
      cy.nodes(".group").forEach((g) => {
        const anyVisible = g.children(".entity").some((c: any) => !c.hasClass("hidden"));
        g.toggleClass("hidden", !anyVisible);
      });
    });
  }, [q, nsFilter, failuresOnly, hideStruct, topo]);

  const counts = useMemo(() => {
    const s = { firing: 0, degraded: 0, loud: 0, warned: 0, total: topo?.nodes.length ?? 0 };
    for (const n of topo?.nodes ?? []) {
      if (n.degraded) s.firing++;
      else if (n.matched) s.degraded++;
      if (n.loud) s.loud++;
      if (n.warned) s.warned++;
    }
    return s;
  }, [topo]);

  function fit() {
    cyRef.current?.animate({
      fit: { eles: cyRef.current.elements(":visible"), padding: 40 },
      duration: 250,
    });
  }
  function relayout() {
    cyRef.current?.layout(FCOSE as any).run();
  }
  function toggleNs(ns: string) {
    setNsFilter((prev) => {
      const next = new Set(prev);
      if (next.has(ns)) next.delete(ns);
      else next.add(ns);
      return next;
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
            placeholder="Search services…"
            className="w-40 bg-transparent text-[12.5px] text-ink outline-none placeholder:text-ink-low"
          />
        </div>
        <Button
          variant="secondary"
          active={failuresOnly}
          onClick={() => setFailuresOnly((v) => !v)}
        >
          <Icon.filter size={13} /> Failures only
        </Button>
        <Button
          variant="secondary"
          active={hideStruct}
          onClick={() => setHideStruct((v) => !v)}
          title="Hide runs-on / placement edges, show dependencies only"
        >
          Dependencies only
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
                  : "border-rule text-ink-mute hover:text-ink-low",
              )}
            >
              {ns}
            </button>
          ))}
        </div>
        <div className="ml-auto flex items-center gap-1.5">
          <Button variant="ghost" onClick={relayout} title="Re-layout">
            <Icon.cluster size={14} /> Relayout
          </Button>
          <Button variant="ghost" onClick={fit} title="Fit to view">
            Fit
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

        {/* legend */}
        <div className="absolute bottom-3 left-3 flex flex-col gap-1.5 rounded-[8px] border border-rule bg-plane/80 px-3 py-2.5 backdrop-blur">
          <div className="v-eyebrow mb-0.5">State</div>
          <LegendRow sev="firing" label="Firing / degraded match" />
          <LegendRow sev="degraded" label="Matched phenomenon" />
          <LegendRow sev="info" label="Unexplained (loud)" />
          <LegendRow sev="ok" label="Healthy" />
          <div className="mt-1 flex items-center gap-2 text-[10.5px] text-ink-low">
            <span
              className="inline-block h-2.5 w-2.5 rounded-full border border-dashed"
              style={{ borderColor: signal.info }}
            />
            warned — projected “might”
          </div>
        </div>

        {/* totals */}
        <div className="absolute top-3 right-3 flex items-center gap-3 rounded-[8px] border border-rule bg-plane/80 px-3 py-2 font-mono text-[11px] backdrop-blur">
          <span className="text-ink-low">{counts.total} nodes</span>
          {counts.firing > 0 && <span style={{ color: signal.error }}>{counts.firing} firing</span>}
          {counts.degraded > 0 && (
            <span style={{ color: signal.warning }}>{counts.degraded} matched</span>
          )}
          {counts.loud > 0 && <span style={{ color: signal.info }}>{counts.loud} loud</span>}
        </div>

        {/* detail panel */}
        {sel && (
          <NodeDetail
            sel={sel}
            onClose={() => {
              setSel(null);
              cyRef.current?.elements().removeClass("dim hl focus");
            }}
          />
        )}
      </div>
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

function NodeDetail({ sel, onClose }: { sel: SelInfo; onClose: () => void }) {
  return (
    <div className="absolute top-3 right-3 z-20 w-72 rounded-[10px] border border-rule-strong bg-surface p-4 shadow-xl">
      <div className="mb-3 flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <StateDot sev={sel.sev} size={9} />
            <span className="truncate font-display text-[15px] font-semibold text-ink">
              {sel.label}
            </span>
          </div>
          <Mono className="text-[11px] text-ink-low">
            {sel.namespace || "cluster"} · {sel.kind}
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
      {sel.phenomena.length > 0 ? (
        <div>
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
      ) : (
        <div className="text-[12px] text-ink-low">No active phenomenon match on this entity.</div>
      )}
    </div>
  );
}
