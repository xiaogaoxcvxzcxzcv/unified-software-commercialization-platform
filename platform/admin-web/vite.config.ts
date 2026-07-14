import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import { readFileSync } from "node:fs";

const devTLSPfx = process.env.PLATFORM_ADMIN_DEV_TLS_PFX;
const devTLSPassphrase = process.env.PLATFORM_ADMIN_DEV_TLS_PFX_PASSWORD;

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    strictPort: false,
    https: devTLSPfx ? {
      pfx: readFileSync(devTLSPfx),
      passphrase: devTLSPassphrase,
    } : undefined,
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8080",
        changeOrigin: false,
      },
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: "./src/test/setup.ts",
    css: true,
  },
});
