import { Logo } from "./components/Brand";
import { CoverageReport } from "./surfaces/CoverageReport";

// The Vigil operator shell (doc 10). One frame: a quiet header carrying the mark,
// and the active surface below. The first (and, in 0b, only) surface is the
// Coverage Report — the honest map shown before any finding.
export default function App() {
  return (
    <div
      style={{ minHeight: "100%", display: "flex", flexDirection: "column" }}
    >
      <header
        style={{
          display: "flex",
          alignItems: "center",
          gap: "var(--space-sm)",
          padding: "var(--space-sm) var(--page-pad)",
          borderBottom: "var(--border-hairline)",
          background: "var(--surface-1)",
          position: "sticky",
          top: 0,
          zIndex: 10,
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
        <span className="v-overline" style={{ marginLeft: "var(--space-xs)" }}>
          Coverage
        </span>
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
        <CoverageReport />
      </main>
    </div>
  );
}
