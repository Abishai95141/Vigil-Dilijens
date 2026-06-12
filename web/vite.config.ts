import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// Vite + React + TS (techstack §10). Cytoscape.js (topology), Apache ECharts
// (timeline with band-shaped PROJECTED rendering), and the Connect-Web typed
// client from /proto are added as their surfaces come online (doc 10 M1+).
export default defineConfig({
  plugins: [react()],
  server: {
    // Allow importing the canonical brand tokens from the repo root
    // (brand/tokens.css is the single source of truth; the web app consumes it).
    fs: {
      allow: [fileURLToPath(new URL("..", import.meta.url))],
    },
    proxy: {
      // The obsd API (doc 10) is served on :9095 in dev; the web app proxies to it.
      "/api": "http://localhost:9095",
    },
  },
  test: {
    environment: "node",
  },
});
