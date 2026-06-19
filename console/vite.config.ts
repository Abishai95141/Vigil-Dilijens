import { URL, fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// Dev proxies both /api and /mcp to obsd (same-origin from the browser's view, so no
// CORS needed in dev). obsd must be running with --api --mcp-enabled. The target defaults
// to :9095; override with VITE_OBSD_TARGET to point the console at any obsd instance.
const obsdTarget = process.env.VITE_OBSD_TARGET ?? "http://127.0.0.1:9095";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  server: {
    port: Number(process.env.PORT) || 5173,
    proxy: {
      "/api": { target: obsdTarget, changeOrigin: true },
      "/mcp": { target: obsdTarget, changeOrigin: true },
    },
  },
  build: { outDir: "dist", sourcemap: true },
});
