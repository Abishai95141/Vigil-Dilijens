// Brand primitives — the reusable building blocks of every Vigil surface, built
// on the design tokens (brand/tokens.css) and the provenance semantics
// (src/provenance.ts). Defined once; the Coverage Report (10 M1) and every later
// surface compose from these so the class visual languages stay identical.

import {
  LADDER,
  type LadderRung,
  PROVENANCE,
  type ProvenanceClass,
} from "../provenance";

/** The Vigil mark — the watcher's aperture holding the three tiers of certainty. */
export function Logo({ size = 28 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 64 64"
      fill="none"
      role="img"
      aria-label="Vigil"
    >
      <title>Vigil</title>
      <rect x="2" y="2" width="60" height="60" rx="16" fill="#0a0d13" />
      <rect
        x="2.5"
        y="2.5"
        width="59"
        height="59"
        rx="15.5"
        stroke="var(--border)"
      />
      <path
        d="M18 20 H46"
        stroke="var(--prov-authored-on-dark)"
        strokeWidth="3"
        strokeLinecap="round"
        strokeDasharray="0.1 7"
      />
      <rect
        x="18"
        y="27"
        width="28"
        height="7"
        rx="3.5"
        fill="var(--prov-projected-on-dark)"
        fillOpacity="0.3"
      />
      <path
        d="M18 30.5 H46"
        stroke="var(--prov-projected-on-dark)"
        strokeWidth="2"
        strokeLinecap="round"
        strokeDasharray="5 4"
      />
      <path
        d="M18 41 H46"
        stroke="var(--prov-measured-on-dark)"
        strokeWidth="3.5"
        strokeLinecap="round"
      />
      <path
        d="M32 41 V50"
        stroke="var(--text)"
        strokeWidth="2"
        strokeLinecap="round"
      />
      <circle cx="32" cy="52.5" r="2.5" fill="var(--text-strong)" />
    </svg>
  );
}

/** A provenance class chip — icon + label, in the class identity. */
export function ProvChip({ cls }: { cls: ProvenanceClass }) {
  const s = PROVENANCE[cls];
  return (
    <span className="v-prov-chip" data-prov={cls} title={s.rule}>
      <span aria-hidden>{s.icon}</span>
      {s.label}
    </span>
  );
}

/** A provenance card — the keystone surface: class-colored left rule + chip. */
export function ProvCard({
  cls,
  title,
  children,
}: {
  cls: ProvenanceClass;
  title?: string;
  children: React.ReactNode;
}) {
  return (
    <article className="v-prov-card" data-prov={cls}>
      <header
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: "var(--space-sm)",
          marginBottom: "var(--space-sm)",
        }}
      >
        {title ? <h4>{title}</h4> : <span />}
        <ProvChip cls={cls} />
      </header>
      {children}
    </article>
  );
}

/** The 4-rung severity ladder for one measured variable (doc 05 §3.4). */
export function Ladder({ rung }: { rung: LadderRung }) {
  const active = LADDER[rung].rank;
  const rungs: LadderRung[] = ["below", "at-threshold", "above", "well-above"];
  return (
    <span
      className="v-ladder"
      role="img"
      aria-label={`severity: ${LADDER[rung].label}`}
    >
      {rungs.map((r) => {
        const on = LADDER[r].rank <= active && active > 0;
        return (
          <span
            key={r}
            className="v-ladder-seg"
            data-on={on}
            style={
              on
                ? ({
                    "--rung-color": `var(${LADDER[rung].token})`,
                  } as React.CSSProperties)
                : undefined
            }
          />
        );
      })}
    </span>
  );
}

export type CoverageState =
  | "full"
  | "partial"
  | "none"
  | "unbounded"
  | "suspect";

/** A coverage cell — the honest-map unit; every (entity, variable) pair has one. */
export function CoverageCell({
  state,
  label,
}: { state: CoverageState; label?: string }) {
  return (
    <span className="v-cov" data-state={state} title={label}>
      <span className="v-cov-dot" />
      {label ?? state}
    </span>
  );
}
