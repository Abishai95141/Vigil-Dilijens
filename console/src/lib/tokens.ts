// JS-accessible mirror of the brand-kit design tokens (src/styles/theme.css).
// Needed where styling happens in JS rather than CSS — Cytoscape graph styles,
// SVG charts, canvas. Keep in lockstep with theme.css.

export const plane = {
  plane: "#0A0A0A",
  surface: "#121212",
  surfaceHi: "#181818",
  mute: "#3F3F3D",
  inkLow: "#6A6A67",
  inkMid: "#A5A5A2",
  inkSoft: "#D4D4D2",
  ink: "#F0EFEC",
  signal: "#FFFFFF",
  rule: "rgba(245,245,244,0.08)",
  ruleStrong: "rgba(245,245,244,0.16)",
} as const;

export const signal = {
  success: "#5FBE8A",
  warning: "#E6B450",
  error: "#EB6B5E",
  info: "#6BA4E5",
  successTint: "rgba(95,190,138,0.14)",
  warningTint: "rgba(230,180,80,0.14)",
  errorTint: "rgba(235,107,94,0.14)",
  infoTint: "rgba(107,164,229,0.14)",
  successEdge: "rgba(95,190,138,0.42)",
  warningEdge: "rgba(230,180,80,0.42)",
  errorEdge: "rgba(235,107,94,0.42)",
  infoEdge: "rgba(107,164,229,0.42)",
} as const;

export const font = {
  display: '"Plus Jakarta Sans", "Geist", system-ui, sans-serif',
  body: '"Geist", "Inter", system-ui, sans-serif',
  mono: '"JetBrains Mono", ui-monospace, monospace',
} as const;

// Operational severity → the single hue the system permits for that state.
// success = healthy/live/resolved · warning = degraded/throttled · error =
// firing/failed/OOM (sparingly) · info = surfaced/notable, non-severe.
export type Severity = "ok" | "info" | "degraded" | "firing" | "neutral";

export function severityColor(s: Severity): string {
  switch (s) {
    case "ok":
      return signal.success;
    case "info":
      return signal.info;
    case "degraded":
      return signal.warning;
    case "firing":
      return signal.error;
    default:
      return plane.inkMid;
  }
}
