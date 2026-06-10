// The three provenance classes (doc 01 §3) — the system's trust spine. Surfaces
// render them in distinct visual languages and NEVER fuse them (doc 10 §3.1).
// Design tokens for the three classes are defined ONCE, here, and used everywhere
// (techstack §10). This module is intentionally tiny and pure so it is unit-testable
// before any UI exists.

export type ProvenanceClass = "MEASURED" | "PROJECTED" | "AUTHORED";

export interface ProvenanceStyle {
  label: string;
  // Register (doc 01 §5): the grammatical mood each class must be phrased in.
  register: "indicative" | "modal" | "attributed";
  token: string; // CSS custom property name for the class color
}

export const PROVENANCE: Record<ProvenanceClass, ProvenanceStyle> = {
  MEASURED: { label: "Measured", register: "indicative", token: "--prov-measured" },
  PROJECTED: { label: "Projected", register: "modal", token: "--prov-projected" },
  AUTHORED: { label: "Authored", register: "attributed", token: "--prov-authored" },
};

export function styleFor(cls: ProvenanceClass): ProvenanceStyle {
  return PROVENANCE[cls];
}
