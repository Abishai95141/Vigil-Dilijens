// The three provenance classes (doc 01 §3) — the system's trust spine. Surfaces
// render them in distinct visual languages and NEVER fuse them (doc 10 §3.1).
// The COLOR/space/type tokens live once in brand/tokens.css; this module is the
// single TS source of the per-class *semantics* — label, register (grammatical
// mood), icon, treatment, and the token names a component should reach for. It is
// intentionally tiny and pure so it is unit-testable before any UI exists.

export type ProvenanceClass = "MEASURED" | "PROJECTED" | "AUTHORED";

// Register (doc 01 §5): the grammatical mood each class MUST be phrased in.
//   indicative — "is" (a fact)         · modal — "might / ~" (a forecast)
//   attributed — "per the graph" (a curated note, verbatim)
export type Register = "indicative" | "modal" | "attributed";

// Treatment is the non-color visual signal, so the class survives greyscale and
// color-vision deficiency (the "color is never the only signal" rule, doc 10 §4).
export type Treatment = "solid" | "band" | "dotted";

export interface ProvenanceStyle {
  label: string;
  register: Register;
  treatment: Treatment;
  icon: string; // a glyph hint; surfaces may swap for an SVG of the same idea
  // CSS custom-property names (defined in brand/tokens.css) — never hex literals.
  token: string; // primary color (use the -on-dark sibling on dark surfaces)
  tintToken: string; // 12% fill for chips/rows
  borderToken: string; // ~36% border
  textToken: string; // accessible text-on-surface variant
  // The one-line discipline a writer/reviewer must honour for this class.
  rule: string;
}

export const PROVENANCE: Record<ProvenanceClass, ProvenanceStyle> = {
  MEASURED: {
    label: "Measured",
    register: "indicative",
    treatment: "solid",
    icon: "▬",
    token: "--prov-measured",
    tintToken: "--prov-measured-tint",
    borderToken: "--prov-measured-border",
    textToken: "--prov-measured-text",
    rule: "A fact from the store or its arithmetic consequence. State it plainly, with the value and its bar.",
  },
  PROJECTED: {
    label: "Projected",
    register: "modal",
    treatment: "band",
    icon: "▤",
    token: "--prov-projected",
    tintToken: "--prov-projected-tint",
    borderToken: "--prov-projected-border",
    textToken: "--prov-projected-text",
    rule: "A forecast against a bar. Always modal, always a band — it never collapses to a line.",
  },
  AUTHORED: {
    label: "Authored",
    register: "attributed",
    treatment: "dotted",
    icon: "❝",
    token: "--prov-authored",
    tintToken: "--prov-authored-tint",
    borderToken: "--prov-authored-border",
    textToken: "--prov-authored-text",
    rule: "A curated graph note, surfaced verbatim with author + version. Never paraphrased into a cause.",
  },
};

export function styleFor(cls: ProvenanceClass): ProvenanceStyle {
  return PROVENANCE[cls];
}

// The 4-rung severity ladder (doc 05 §3.4) — the state of ONE measured variable.
// A sub-language INSIDE measured content; never a standalone class mark.
export type LadderRung =
  | "unknown"
  | "below"
  | "at-threshold"
  | "above"
  | "well-above";

export const LADDER: Record<
  LadderRung,
  { label: string; token: string; rank: number }
> = {
  unknown: { label: "unknown", token: "--rung-unknown", rank: 0 },
  below: { label: "below", token: "--rung-below", rank: 1 },
  "at-threshold": {
    label: "at threshold",
    token: "--rung-at-threshold",
    rank: 2,
  },
  above: { label: "above", token: "--rung-above", rank: 3 },
  "well-above": { label: "well above", token: "--rung-well-above", rank: 4 },
};
