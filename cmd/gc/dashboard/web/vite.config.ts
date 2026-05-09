import { defineConfig } from "vite";

// Single-bundle build: one JS, one CSS, one HTML. The Go static server
// embeds dist/ via go:embed and ships it verbatim, so predictable asset
// filenames (no hashing) keep the embedding simple.
export default defineConfig({
  build: {
    outDir: "dist",
    emptyOutDir: true,
    assetsDir: ".",
    rollupOptions: {
      output: {
        entryFileNames: "dashboard.js",
        chunkFileNames: "dashboard.js",
        assetFileNames: "[name][extname]",
      },
    },
  },
  server: {
    port: 5173,
    strictPort: true,
    // Local-dev only: proxy supervisor calls so the SPA can use relative
    // URLs (matches the "Same-origin-only dashboards (dev with the Vite
    // proxy)" path described in src/api.ts). Set GC_SUPERVISOR_URL to
    // override the default 127.0.0.1:8372.
    proxy: {
      "/v0": {
        target: process.env.GC_SUPERVISOR_URL ?? "http://127.0.0.1:8372",
        changeOrigin: true,
        ws: true,
      },
      "/schema": {
        target: process.env.GC_SUPERVISOR_URL ?? "http://127.0.0.1:8372",
        changeOrigin: true,
      },
    },
  },
});
