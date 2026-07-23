import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import { readFileSync } from "node:fs";

const loopbackAPI = "http://127.0.0.1:8080";

export default defineConfig(({ mode, isPreview }) => {
	const environment = loadEnv(mode, ".", "HOSTED_");
	const backendTarget = controlledBackendTarget(environment.HOSTED_BACKEND_TARGET?.trim() || loopbackAPI);
	const apiProxy = () => ({ "/api": { target: backendTarget, changeOrigin: false, secure: true } });
	const tlsPFX = environment.HOSTED_DEV_TLS_PFX?.trim();
	const https = tlsPFX ? { pfx: readFileSync(tlsPFX), passphrase: environment.HOSTED_DEV_TLS_PFX_PASSWORD } : undefined;
  const headers = {
    "Cache-Control": "no-store",
		"Content-Security-Policy": `default-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; object-src 'none'; img-src 'self' data: https:; style-src 'self'${isPreview ? "" : " 'nonce-hosted-vite-dev'"}; script-src 'self'; connect-src 'self'`,
    "Permissions-Policy": "camera=(), microphone=(), geolocation=(), payment=(), usb=()",
    "Referrer-Policy": "no-referrer",
    "X-Content-Type-Options": "nosniff",
    "X-Frame-Options": "DENY",
  };
  return {
    plugins: [react(), cspDevRuntime(Boolean(isPreview))],
		resolve: { dedupe: ["react", "react-dom"] },
    build: { target: "es2022", sourcemap: false },
		server: { port: 5175, strictPort: true, https, headers, cors: false, hmr: false, proxy: apiProxy() },
		preview: { port: 4175, strictPort: true, https, headers, cors: false, proxy: apiProxy() },
    test: { environment: "jsdom", setupFiles: "./src/test/setup.ts", css: true, fileParallelism: false },
  };
});

function cspDevRuntime(isPreview: boolean) {
  // Plain Vite dev is loopback-only; the HTTPS launcher uses build + preview and never relies on this fixed development nonce.
  return {
    name: "hosted:csp-dev-runtime",
    apply: "serve" as const,
    transformIndexHtml: {
      order: "post" as const,
      handler(html: string) {
        if (isPreview) return html;
        return html
          .replace(/\s*<script type="module" src="\/@vite\/client"><\/script>\s*/, "\n")
          .replace("</head>", '    <meta property="csp-nonce" nonce="hosted-vite-dev" />\n  </head>');
      },
    },
  };
}

export function controlledBackendTarget(raw: string): string {
  let value: URL;
  try {
    value = new URL(raw);
  } catch {
    throw new Error("HOSTED_BACKEND_TARGET must be an exact loopback HTTP origin");
  }
  if (value.username || value.password || value.pathname !== "/" || value.search || value.hash) throw new Error("HOSTED_BACKEND_TARGET must be an exact loopback HTTP origin");
  const loopback = value.hostname === "127.0.0.1" || value.hostname === "localhost" || value.hostname === "[::1]";
  if (value.protocol !== "http:" || !loopback) throw new Error("HOSTED_BACKEND_TARGET must be an exact loopback HTTP origin");
  return value.origin;
}
