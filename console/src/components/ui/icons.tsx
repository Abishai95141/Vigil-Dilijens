import type { SVGProps } from "react";

// Outline icons, ~1.2px stroke on a 24 grid — architectural, concentric/axial.
// fill is reserved for live "now" state (handled by callers), per the brand kit.

type P = SVGProps<SVGSVGElement> & { size?: number };

function Svg({ size = 18, children, ...rest }: P & { children: React.ReactNode }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.4}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
      {...rest}
    >
      {children}
    </svg>
  );
}

export const Icon = {
  graph: (p: P) => (
    <Svg {...p}>
      <circle cx="6" cy="7" r="2" />
      <circle cx="18" cy="6" r="2" />
      <circle cx="16" cy="18" r="2" />
      <path d="M8 7.4l8-1.1M7.4 8.6l8 8M16.6 8l-.5 8" />
    </Svg>
  ),
  cluster: (p: P) => (
    <Svg {...p}>
      <circle cx="12" cy="12" r="8.5" />
      <circle cx="12" cy="12" r="1.6" fill="currentColor" stroke="none" />
    </Svg>
  ),
  node: (p: P) => (
    <Svg {...p}>
      <rect x="4" y="4" width="7" height="7" rx="1" />
      <rect x="13" y="4" width="7" height="7" rx="1" />
      <rect x="4" y="13" width="7" height="7" rx="1" />
      <rect x="13" y="13" width="7" height="7" rx="1" />
    </Svg>
  ),
  pod: (p: P) => (
    <Svg {...p}>
      <rect x="4.5" y="6" width="15" height="12" rx="1.5" />
      <path d="M4.5 9.5h15" />
    </Svg>
  ),
  agent: (p: P) => (
    <Svg {...p}>
      <circle cx="12" cy="6" r="2" />
      <circle cx="6" cy="17" r="2" />
      <circle cx="18" cy="17" r="2" />
      <path d="M11 7.6l-4 7.6M13 7.6l4 7.6M8 17h8" />
    </Svg>
  ),
  anomaly: (p: P) => (
    <Svg {...p}>
      <path d="M3 13h3l2-6 3 11 2.5-7 1.5 2H21" />
    </Svg>
  ),
  evidence: (p: P) => (
    <Svg {...p}>
      <path d="M7 4h7l4 4v12H7z" />
      <path d="M13.5 4v4.5H18" />
      <path d="M9.5 13h6M9.5 16h4" />
    </Svg>
  ),
  timeline: (p: P) => (
    <Svg {...p}>
      <path d="M3 12h18" />
      <circle cx="8" cy="12" r="1.6" />
      <circle cx="15" cy="12" r="1.6" fill="currentColor" stroke="none" />
    </Svg>
  ),
  ask: (p: P) => (
    <Svg {...p}>
      <path d="M5 5h14v10H10l-4 4v-4H5z" />
    </Svg>
  ),
  storage: (p: P) => (
    <Svg {...p}>
      <ellipse cx="12" cy="6" rx="7" ry="2.5" />
      <path d="M5 6v12c0 1.4 3.1 2.5 7 2.5s7-1.1 7-2.5V6" />
      <path d="M5 12c0 1.4 3.1 2.5 7 2.5s7-1.1 7-2.5" />
    </Svg>
  ),
  schema: (p: P) => (
    <Svg {...p}>
      <path d="M12 3l8 4.5-8 4.5-8-4.5z" />
      <path d="M4 12l8 4.5 8-4.5M4 16.5L12 21l8-4.5" />
    </Svg>
  ),
  forecast: (p: P) => (
    <Svg {...p}>
      <path d="M3 16c4-1 6-9 9-9s4 4 9 3" />
      <path d="M3 19c4-1 6-7 9-7s4 3 9 2" strokeDasharray="2 2" opacity="0.6" />
    </Svg>
  ),
  coverage: (p: P) => (
    <Svg {...p}>
      <rect x="4" y="4" width="16" height="16" rx="1.5" />
      <path d="M4 10h16M10 4v16" />
      <path d="M13 13l2 2 3-3.5" />
    </Svg>
  ),
  shield: (p: P) => (
    <Svg {...p}>
      <path d="M12 3l7 3v6c0 4-3 6.5-7 9-4-2.5-7-5-7-9V6z" />
      <path d="M9 12l2 2 4-4.5" />
    </Svg>
  ),
  mcp: (p: P) => (
    <Svg {...p}>
      <circle cx="6" cy="12" r="2.5" />
      <circle cx="18" cy="6" r="2.5" />
      <circle cx="18" cy="18" r="2.5" />
      <path d="M8.2 10.8l7.6-3.6M8.2 13.2l7.6 3.6" />
    </Svg>
  ),
  config: (p: P) => (
    <Svg {...p}>
      <circle cx="12" cy="12" r="3" />
      <path d="M12 3v3M12 18v3M3 12h3M18 12h3M5.5 5.5l2 2M16.5 16.5l2 2M18.5 5.5l-2 2M7.5 16.5l-2 2" />
    </Svg>
  ),
  search: (p: P) => (
    <Svg {...p}>
      <circle cx="11" cy="11" r="6.5" />
      <path d="M16 16l4 4" />
    </Svg>
  ),
  filter: (p: P) => (
    <Svg {...p}>
      <path d="M4 6h16l-6 7v5l-4 2v-7z" />
    </Svg>
  ),
  close: (p: P) => (
    <Svg {...p}>
      <path d="M6 6l12 12M18 6L6 18" />
    </Svg>
  ),
  chevron: (p: P) => (
    <Svg {...p}>
      <path d="M9 6l6 6-6 6" />
    </Svg>
  ),
  incident: (p: P) => (
    <Svg {...p}>
      <path d="M12 4v8M12 16v.5" />
      <circle cx="12" cy="12" r="8.5" />
    </Svg>
  ),
  silence: (p: P) => (
    <Svg {...p}>
      <path d="M4 9v6h4l5 4V5L8 9z" />
      <path d="M16 9l4 6M20 9l-4 6" />
    </Svg>
  ),
  dot: (p: P) => (
    <Svg {...p}>
      <circle cx="12" cy="12" r="3" fill="currentColor" stroke="none" />
    </Svg>
  ),
  logo: (p: P) => (
    <Svg {...p} strokeWidth={1.3}>
      <circle cx="12" cy="12" r="9" />
      <circle cx="12" cy="12" r="2" fill="currentColor" stroke="none" />
    </Svg>
  ),
};

export type IconName = keyof typeof Icon;
