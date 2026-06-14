import { useState } from "react";
import { Logo } from "./components/Brand";
import { Chat } from "./surfaces/Chat";
import { Config } from "./surfaces/Config";
import { ContextWindows } from "./surfaces/ContextWindows";
import { CoverageReport } from "./surfaces/CoverageReport";
import { CrossService } from "./surfaces/CrossService";
import { EarlyWarnings } from "./surfaces/EarlyWarnings";
import { InsightFeed } from "./surfaces/InsightFeed";
import { OnCall } from "./surfaces/OnCall";
import { Timeline } from "./surfaces/Timeline";
import { TopologyView } from "./surfaces/TopologyView";
import { UnexplainedSurface } from "./surfaces/UnexplainedSurface";

// The Vigil operator shell (doc 10). One frame: a quiet header carrying the mark,
// a tab strip across the surfaces, and the active surface below. The surfaces are
// the join — the only place the three provenance classes meet, each still
// wearing its label (doc 10 §2). M2–M4 add the now/topology/anomaly views beside
// the M1 coverage map; early warnings (PROJECTED) arrive in Phase 2.

type Tab =
  | "insights"
  | "warnings"
  | "topology"
  | "crossservice"
  | "unexplained"
  | "timeline"
  | "coverage"
  | "context"
  | "ask"
  | "oncall"
  | "config";

const TABS: { id: Tab; label: string }[] = [
  { id: "insights", label: "Insights" },
  { id: "warnings", label: "Early warnings" },
  { id: "topology", label: "Topology" },
  { id: "crossservice", label: "Cross-service" },
  { id: "unexplained", label: "Unexplained" },
  { id: "timeline", label: "Timeline" },
  { id: "coverage", label: "Coverage" },
  { id: "context", label: "Context windows" },
  { id: "ask", label: "Ask" },
  { id: "oncall", label: "On-call" },
  { id: "config", label: "Config" },
];

export default function App() {
  const [tab, setTab] = useState<Tab>("insights");
  return (
    <div
      style={{ minHeight: "100%", display: "flex", flexDirection: "column" }}
    >
      <header
        style={{
          display: "flex",
          alignItems: "center",
          gap: "var(--space-md)",
          padding: "var(--space-sm) var(--page-pad)",
          borderBottom: "var(--border-hairline)",
          background: "var(--surface-1)",
          position: "sticky",
          top: 0,
          zIndex: 10,
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "var(--space-sm)",
          }}
        >
          <Logo size={28} />
          <span
            style={{
              fontFamily: "var(--font-heading)",
              fontWeight: "var(--weight-bold)",
              fontSize: "var(--text-h3)",
              color: "var(--text-strong)",
              letterSpacing: "var(--tracking-tight)",
            }}
          >
            Vigil
          </span>
        </div>
        <nav className="v-tabs" style={{ marginLeft: "var(--space-md)" }}>
          {TABS.map((t) => (
            <button
              key={t.id}
              type="button"
              className="v-tab"
              data-active={tab === t.id}
              onClick={() => setTab(t.id)}
            >
              {t.label}
            </button>
          ))}
        </nav>
      </header>
      <main
        style={{
          flex: 1,
          width: "100%",
          maxWidth: 1200,
          margin: "0 auto",
          padding: "var(--page-pad)",
        }}
      >
        {tab === "insights" && <InsightFeed />}
        {tab === "warnings" && <EarlyWarnings />}
        {tab === "topology" && <TopologyView />}
        {tab === "crossservice" && <CrossService />}
        {tab === "unexplained" && <UnexplainedSurface />}
        {tab === "timeline" && <Timeline />}
        {tab === "coverage" && <CoverageReport />}
        {tab === "context" && <ContextWindows />}
        {tab === "ask" && <Chat />}
        {tab === "oncall" && <OnCall />}
        {tab === "config" && <Config />}
      </main>
    </div>
  );
}
