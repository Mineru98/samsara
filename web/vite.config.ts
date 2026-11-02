/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The runtime embeds the build output (runtime/internal/webui) and serves it
// same-origin with the API, so production needs no proxy and no CDN.
// In dev, Vite proxies /v1 to a running daemon (SAMSARA_UI_PORT) so the bearer
// and SSE flow stay same-origin as in production (02 §21.2).
const runtimePort = process.env.SAMSARA_UI_PORT ?? "47690";

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../runtime/internal/webui/static",
    emptyOutDir: true,
    assetsInlineLimit: 0,
    sourcemap: false,
  },
  worker: { format: "es" },
  server: {
    proxy: {
      "/v1": {
        target: `http://127.0.0.1:${runtimePort}`,
        changeOrigin: true,
        headers: { Origin: `http://127.0.0.1:${runtimePort}` },
      },
    },
  },
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.{ts,tsx}"],
  },
});
