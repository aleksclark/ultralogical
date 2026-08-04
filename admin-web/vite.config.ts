import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";

// Dev/proxy path: browser talks to Vite (:5173); API proxied to coreadmin.
// CORE_ADMIN_URL defaults to local coreadmin. Playwright uses the same proxy.
const adminTarget = process.env.CORE_ADMIN_URL || "http://127.0.0.1:8082";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "src"),
      "@admin-gen": path.resolve(__dirname, "src/gen"),
    },
  },
  server: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    proxy: {
      // Connect-RPC paths and health probes.
      "/admin.v1.": {
        target: adminTarget,
        changeOrigin: true,
      },
      "/healthz": { target: adminTarget, changeOrigin: true },
      "/readyz": { target: adminTarget, changeOrigin: true },
    },
  },
  preview: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    proxy: {
      "/admin.v1.": {
        target: adminTarget,
        changeOrigin: true,
      },
      "/healthz": { target: adminTarget, changeOrigin: true },
      "/readyz": { target: adminTarget, changeOrigin: true },
    },
  },
  build: {
    sourcemap: true,
    chunkSizeWarningLimit: 900,
  },
});
