import { URL, fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// Dev proxies both /api and /mcp to obsd on :9095 (same-origin from the browser's
// view, so no CORS needed in dev). obsd must be running with --api --mcp-enabled.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  server: {
    port: 5173,
    proxy: {
      "/api": { target: "http://127.0.0.1:9095", changeOrigin: true },
      "/mcp": { target: "http://127.0.0.1:9095", changeOrigin: true },
    },
  },
  build: { outDir: "dist", sourcemap: true },
});
