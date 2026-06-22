// Root cause & cascades (NOW/SOON) — /api/root-cause-chain + /api/cross-service.
// The headline capability. The transitive chain stitches MEASURED-degraded
// workloads into an ORDERED chain over observed-flow edges, oriented ONLY by the
// AUTHORED relation — coincident-but-unrelated faults never merge. Each lane is
// shown with its own provenance; the PROJECTED cap-D ripple is withheld until its
// live 2-hop gate flips, and that withholding reason is rendered verbatim.

import { useCrossService, useRootCauseChain } from "@/api/client";
import type { Chain } from "@/api/types";
import { LaneNote, ProvChip, SectionHead } from "@/components/ui/primitives";
import { DataState, JoinHint, Page, Stat4 } from "@/components/ui/widgets";
import { ChainFlow } from "@/components/viz/ChainFlow";

function depth(c: Chain): number {
  return (c.path?.length ?? c.chain?.length ?? 0) + 1;
}

function ChainPanel({ c, projected }: { c: Chain; projected?: boolean }) {
  const gaps = c.gaps ?? [];
  const suspect = (c.path ?? []).filter((s) => s.edge_traversal === "suspect").length;
  return (
    <div className="v-panel p-4">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <ProvChip kind={projected ? "PROJECTED" : "MEASURED"} />
        <span className="font-mono text-[10.5px] text-ink-low">{c.node_class}</span>
        <span className="ml-auto font-mono text-[10.5px] text-ink-low">
          depth {depth(c)} · {suspect} suspect · {gaps.length} gap
        </span>
      </div>
      <ChainFlow chain={c} projected={projected} />
      {c.coverage_gaps && (
        <div className="mt-3 border-t border-rule pt-2 font-mono text-[10.5px] text-ink-low">
          flow coverage — resolvable {c.coverage_gaps.resolvable_flows} · SNAT-masked{" "}
          {c.coverage_gaps.snat_masked_flows} · unresolved {c.coverage_gaps.unresolved_flows}
        </div>
      )}
    </div>
  );
}

export function RootCausePage() {
  const rc = useRootCauseChain();
  const xs = useCrossService();

  return (
    <Page>
      <SectionHead
        num="04"
        title="Root cause & cascades"
        lede="The transitive root-cause chain over observed flow, oriented by the authored relation — a chain reaction, connected. MEASURED edges and AUTHORED “why” sit side-by-side; nothing here derives a cause the graph did not author."
        right={<JoinHint />}
      />

      <div className="flex flex-col gap-6">
        {/* Transitive root-cause chain (MEASURED ⋈ AUTHORED) */}
        <div>
          <div className="v-eyebrow mb-2">
            Transitive chain — observed flow ⋈ authored orientation
          </div>
          <DataState q={rc} skeletonRows={2}>
            {(d) => (
              <div className="flex flex-col gap-3">
                {!d.enabled ? (
                  <LaneNote kind="off" title="Flow discovery is off" note={d.note} />
                ) : d.active && d.chains?.length ? (
                  <>
                    <Stat4
                      items={[
                        { label: "chains", value: d.chains.length, sev: "firing" },
                        {
                          label: "longest",
                          value: Math.max(...d.chains.map(depth)),
                        },
                        {
                          label: "suspect edges",
                          value: d.chains.reduce(
                            (n, c) =>
                              n +
                              (c.path ?? []).filter((s) => s.edge_traversal === "suspect").length,
                            0,
                          ),
                        },
                        {
                          label: "gaps",
                          value: d.chains.reduce((n, c) => n + (c.gaps?.length ?? 0), 0),
                        },
                      ]}
                    />
                    {d.chains.map((c, i) => (
                      <ChainPanel key={i} c={c} />
                    ))}
                  </>
                ) : (
                  <LaneNote kind="empty" title="No transitive chain this tick" note={d.note} />
                )}
              </div>
            )}
          </DataState>
        </div>

        {/* PROJECTED cap-D multi-hop ripple (gate-pending) */}
        <div>
          <div className="v-eyebrow mb-2">Projected ripple — forecast the cascade (cap. D)</div>
          <DataState q={rc} skeletonRows={1}>
            {(d) =>
              d.projectedActive && d.projectedChains?.length ? (
                <div className="flex flex-col gap-3">
                  {d.projectedChains.map((c, i) => (
                    <ChainPanel key={i} c={c} projected />
                  ))}
                </div>
              ) : (
                <LaneNote
                  kind={d.projectedGatePassed ? "empty" : "pending"}
                  title={
                    d.projectedGatePassed
                      ? "Lane live (PROJECTED) — no multi-hop ripple this tick"
                      : "Computed every tick — withheld until the live 2-hop gate flips"
                  }
                  note={d.projectedNote}
                />
              )
            }
          </DataState>
        </div>

        {/* Cross-service cascade — now (MEASURED) + soon (PROJECTED) */}
        <div>
          <div className="v-eyebrow mb-2">
            Cross-service cascade — impact against the call arrow
          </div>
          <DataState q={xs} skeletonRows={1}>
            {(d) => (
              <div className="flex flex-col gap-3">
                {d.active && d.chain ? (
                  <ChainPanel c={d.chain} />
                ) : (
                  <LaneNote
                    kind={d.enabled ? "empty" : "off"}
                    title={d.enabled ? "No cross-service cascade firing" : "Flow discovery off"}
                    note={d.note}
                  />
                )}
                {d.projectedActive && d.projectedChain ? (
                  <ChainPanel c={d.projectedChain} projected />
                ) : (
                  <LaneNote
                    kind={d.projectedGatePassed ? "empty" : "pending"}
                    title={
                      d.projectedGatePassed
                        ? "Anticipatory cross-service lane (PROJECTED) — none this tick"
                        : "Anticipatory cross-service lane"
                    }
                    note={d.projectedNote}
                  />
                )}
              </div>
            )}
          </DataState>
        </div>
      </div>
    </Page>
  );
}
