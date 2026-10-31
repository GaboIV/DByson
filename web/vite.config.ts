import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  build: { outDir: "dist", emptyOutDir: true },
  server: {
    // `go run ./cmd/dbyson` serves the API on :8080 during development; DBYSON_API_URL
    // points the proxy elsewhere when that port is taken. The browser's Host is kept so the
    // API's same-origin check sees the page's own origin.
    proxy: { "/api": { target: process.env.DBYSON_API_URL || "http://127.0.0.1:8080", changeOrigin: false } },
  },
});
