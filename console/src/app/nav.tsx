import { Icon, type IconName } from "@/components/ui/icons";

export type NavItem = { to: string; label: string; icon: IconName };
export type NavGroup = { label: string; items: NavItem[] };

// Intent-grouped navigation (frontend-spec §2). The cluster graph is the home.
export const NAV: NavGroup[] = [
  {
    label: "",
    items: [
      { to: "/", label: "Cluster graph", icon: "graph" },
      { to: "/overview", label: "Overview", icon: "cluster" },
    ],
  },
  {
    label: "Now",
    items: [
      { to: "/insights", label: "Insights", icon: "evidence" },
      { to: "/root-cause", label: "Root cause", icon: "agent" },
      { to: "/events", label: "Events", icon: "pod" },
    ],
  },
  {
    label: "Soon",
    items: [
      { to: "/forecast", label: "Early warnings", icon: "forecast" },
      { to: "/anomalies", label: "Anomalies", icon: "anomaly" },
    ],
  },
  {
    label: "Memory",
    items: [
      { to: "/incidents", label: "Incidents", icon: "incident" },
      { to: "/timeline", label: "Timeline", icon: "timeline" },
    ],
  },
  {
    label: "Truth",
    items: [
      { to: "/coverage", label: "Coverage", icon: "coverage" },
      { to: "/silence", label: "Silence ledger", icon: "silence" },
      { to: "/governance", label: "Governance", icon: "schema" },
      { to: "/causal-hypotheses", label: "Causal hypotheses", icon: "agent" },
      { to: "/referee", label: "Referee", icon: "shield" },
      { to: "/config", label: "Config", icon: "config" },
      { to: "/integrations", label: "MCP", icon: "mcp" },
    ],
  },
];

export function NavIcon({ name, size }: { name: IconName; size?: number }) {
  const C = Icon[name];
  return <C size={size} />;
}
