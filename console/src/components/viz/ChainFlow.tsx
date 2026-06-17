// ChainFlow — renders a flow.Chain (snake_case) as a directed root→downstream
// flow. The chain JOINS a MEASURED observed-flow edge, the MEASURED degradation
// finding, and the AUTHORED relation "why" — each shown separately, never fused.
// Root carries a ring (most_upstream_degraded_node, a MEASURED structural fact,
// not a cause). Suspect edges render dashed. A PROJECTED band (cap. D) widens per
// hop and never collapses. Silent-intermediate gaps render as honest ghost breaks.

import type { Chain, ChainGap, PathStep, ProjectedBand } from "@/api/types";
import { ProvChip, StateDot } from "@/components/ui/primitives";
import { AuthoredNote, Tag } from "@/components/ui/widgets";
import { clock } from "@/lib/format";

function Node({
  label,
  phenomenon,
  root,
  projected,
}: {
  label: string;
  phenomenon?: string;
  root?: boolean;
  projected?: boolean;
}) {
  return (
    <div
      className="v-panel-inset flex min-w-0 items-center gap-2.5 px-3 py-2"
      style={root ? { boxShadow: "0 0 0 1px var(--color-rule-strong)" } : undefined}
    >
      <StateDot sev={projected ? "info" : "firing"} live={root} size={9} />
      <div className="min-w-0">
        <div className="truncate text-[12.5px] font-medium text-ink">{label}</div>
        {phenomenon && (
          <div className="v-mono truncate text-[10.5px] text-ink-low">{phenomenon}</div>
        )}
      </div>
      {root && (
        <span className="ml-auto shrink-0 font-mono text-[9px] tracking-[0.1em] text-ink-low uppercase">
          root
        </span>
      )}
    </div>
  );
}

function BandBadge({ band }: { band: ProjectedBand }) {
  return (
    <div className="v-prov--projected mt-1 rounded-[4px] px-2 py-1 text-[10.5px] not-italic">
      <span className="font-mono tracking-[0.04em] text-ink-mid">
        {clock(band.earliest)} … {band.open ? "open" : clock(band.latest)}
      </span>
      <span className="ml-2 text-ink-low">· hop {band.hops_from_root}</span>
      {band.widen_note && <div className="mt-0.5 text-ink-low">{band.widen_note}</div>}
    </div>
  );
}

function Connector({
  why,
  author,
  version,
  suspect,
  temporal,
}: {
  why?: string;
  author?: string;
  version?: string;
  suspect?: boolean;
  temporal?: string;
}) {
  return (
    <div className="flex gap-3 py-1.5 pl-4">
      {/* the edge spine */}
      <div className="flex flex-col items-center pt-1">
        <div
          className="w-px flex-1"
          style={{
            minHeight: 18,
            background: suspect
              ? "repeating-linear-gradient(var(--color-warning) 0 3px, transparent 3px 6px)"
              : "var(--color-rule-strong)",
          }}
        />
        <svg width="9" height="7" viewBox="0 0 9 7" aria-hidden>
          <path
            d="M4.5 7L0 0h9z"
            fill={suspect ? "var(--color-warning)" : "var(--color-ink-low)"}
          />
        </svg>
      </div>
      <div className="min-w-0 flex-1 pb-1">
        <div className="mb-1 flex items-center gap-2">
          <ProvChip kind="MEASURED" title="observed-flow edge" />
          <span className="font-mono text-[9.5px] tracking-[0.06em] text-ink-low">
            observed flow{temporal ? ` · ${temporal}` : ""}
          </span>
          {suspect && <Tag sev="degraded">suspect</Tag>}
        </div>
        {why && (
          <AuthoredNote source={author ? `${author}${version ? `@${version}` : ""}` : undefined}>
            {why}
          </AuthoredNote>
        )}
      </div>
    </div>
  );
}

function GapBreak({ gap }: { gap: ChainGap }) {
  return (
    <div className="flex gap-3 py-1.5 pl-4">
      <div className="flex flex-col items-center pt-1">
        <div
          className="w-px flex-1"
          style={{
            minHeight: 18,
            background:
              "repeating-linear-gradient(var(--color-ink-low) 0 2px, transparent 2px 5px)",
          }}
        />
      </div>
      <div className="min-w-0 flex-1 pb-1">
        <div className="rounded-[6px] border border-dashed border-rule-strong px-3 py-2 text-[11.5px] text-ink-low">
          <span className="font-mono text-[9.5px] tracking-[0.1em] text-ink-low uppercase">
            gap · never bridged
          </span>
          <div className="mt-0.5">{gap.reason}</div>
        </div>
      </div>
    </div>
  );
}

/** Render a transitive `path` (cap. B/D) as an ordered root→downstream flow. */
export function ChainFlow({ chain, projected }: { chain: Chain; projected?: boolean }) {
  const path = chain.path ?? [];
  const gaps = chain.gaps ?? [];

  if (path.length === 0) {
    // one-hop (cross-service) shape: render the impacted←degraded links instead.
    const links = chain.chain ?? [];
    return (
      <div className="flex flex-col gap-0">
        <Node label={chain.most_upstream_degraded_node} root projected={projected} />
        {links.map((l, i) => (
          <div key={i}>
            <Connector
              why={l.why}
              author={l.author}
              version={l.version}
              temporal={l.temporal}
              suspect={l.edge_traversal === "suspect"}
            />
            <Node label={l.impacted} projected={projected} />
          </div>
        ))}
        {gaps.map((g, i) => (
          <GapBreak key={`g${i}`} gap={g} />
        ))}
      </div>
    );
  }

  // ordered transitive chain (cap. B): root is path[0].upstream.
  const steps: PathStep[] = path;
  return (
    <div className="flex flex-col gap-0">
      <Node
        label={steps[0].upstream}
        phenomenon={steps[0].upstream_phenomenon}
        root
        projected={projected}
      />
      {steps.map((s, i) => (
        <div key={i}>
          <Connector
            why={s.why}
            author={s.author}
            version={s.version}
            temporal={s.temporal}
            suspect={s.edge_traversal === "suspect"}
          />
          <Node label={s.downstream} phenomenon={s.downstream_phenomenon} projected={projected} />
          {s.band && <BandBadge band={s.band} />}
        </div>
      ))}
      {gaps.map((g, i) => (
        <GapBreak key={`g${i}`} gap={g} />
      ))}
    </div>
  );
}
