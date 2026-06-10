import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// Vite + React + TS (techstack §10). Cytoscape.js (topology), Apache ECharts
// (timeline with band-shaped PROJECTED rendering), Tailwind, and the Connect-Web
// typed client from /proto are added as their surfaces come online (doc 10 M1+).
export default defineConfig({
  plugins: [react()],
  test: {
    environment: "node",
  },
});
