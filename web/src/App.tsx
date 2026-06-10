import { PROVENANCE, type ProvenanceClass } from "./provenance";

// Scaffold placeholder. The real first surface is the Coverage Report (doc 10 M1) —
// the honest map of what the system can and cannot watch — which renders before any
// finding does. This component just demonstrates the three-class visual separation.
const CLASSES: ProvenanceClass[] = ["MEASURED", "PROJECTED", "AUTHORED"];

export default function App() {
  return (
    <main style={{ fontFamily: "system-ui, sans-serif", padding: "2rem", maxWidth: 720 }}>
      <h1>Vigil</h1>
      <p>Kubernetes AI Observability — curated ontology graph + narrow forecasting clock.</p>
      <p>
        <em>Scaffold.</em> First surface to build: the Coverage Report (doc 10 M1).
      </p>
      <h2>Provenance classes (never fused)</h2>
      <ul>
        {CLASSES.map((c) => (
          <li key={c}>
            <strong>{PROVENANCE[c].label}</strong> — register: {PROVENANCE[c].register}
          </li>
        ))}
      </ul>
    </main>
  );
}
