// @vitest-environment node
import { afterEach, describe, expect, it } from "vitest";
import { build, createServer as createViteServer, loadConfigFromFile, preview, type HttpServer } from "vite";
import { createServer as createHTTPServer, type Server } from "node:http";
import { spawn, spawnSync } from "node:child_process";
import { existsSync, lstatSync, mkdtempSync, readFileSync, readdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { controlledBackendTarget } from "../vite.config";

const configFile = resolve(import.meta.dirname, "../vite.config.ts");
const appRoot = resolve(import.meta.dirname, "..");
const environmentNames = ["HOSTED_BACKEND_TARGET", "HOSTED_DEV_TLS_PFX", "HOSTED_DEV_TLS_PFX_PASSWORD"] as const;
const originalEnvironment = new Map(environmentNames.map((name) => [name, process.env[name]]));
const temporaryRoots: string[] = [];

afterEach(() => {
  for (const name of environmentNames) {
    const original = originalEnvironment.get(name);
    if (original === undefined) delete process.env[name];
    else process.env[name] = original;
  }
  for (const root of temporaryRoots.splice(0)) rmSync(root, { recursive: true, force: true, maxRetries: 20, retryDelay: 250 });
});

describe("hosted Vite backend boundary", () => {
  it.each([
    ["http://127.0.0.1:8080", "http://127.0.0.1:8080"],
    ["http://localhost:9080", "http://localhost:9080"],
    ["http://[::1]:8080", "http://[::1]:8080"],
  ])("accepts the exact loopback HTTP origin %s", (input, expected) => {
    expect(controlledBackendTarget(input)).toBe(expected);
  });

  it.each([
    "https://127.0.0.1:8080",
    "http://example.test:8080",
    "http://localhost.example.test:8080",
    "http://user:secret@127.0.0.1:8080",
    "http://127.0.0.1:8080/api",
    "http://127.0.0.1:8080/?query=1",
    "not-a-url",
  ])("rejects a non-loopback or non-origin backend target %s", (input) => {
    expect(() => controlledBackendTarget(input)).toThrow("HOSTED_BACKEND_TARGET");
  });

  it("loads the real config with isolated secure loopback proxies in dev and preview", async () => {
    process.env.HOSTED_BACKEND_TARGET = "http://localhost:9080";
    const loaded = await loadConfigFromFile({ command: "serve", mode: "test", isSsrBuild: false, isPreview: false }, configFile);
    expect(loaded?.config.server?.proxy?.["/api"]).toMatchObject({ target: "http://localhost:9080", changeOrigin: false, secure: true });
    expect(loaded?.config.preview?.proxy?.["/api"]).toMatchObject({ target: "http://localhost:9080", changeOrigin: false, secure: true });
    expect(loaded?.config.server?.proxy).not.toBe(loaded?.config.preview?.proxy);
    expect(loaded?.config.server?.proxy?.["/api"]).not.toBe(loaded?.config.preview?.proxy?.["/api"]);
    expect(loaded?.config.server?.hmr).toBe(false);
    expect(loaded?.config.server?.cors).toBe(false);
    expect(loaded?.config.preview?.cors).toBe(false);
    expect(loaded?.config.build?.sourcemap).toBe(false);
    expect(loaded?.config.server?.headers?.["Content-Security-Policy"]).toContain("'nonce-hosted-vite-dev'");
    const loadedPreview = await loadConfigFromFile({ command: "serve", mode: "test", isSsrBuild: false, isPreview: true }, configFile);
    expect(loadedPreview?.config.preview?.headers?.["Content-Security-Policy"]).not.toContain("nonce-");
  });

  it("fails while loading the real config when the environment points outside loopback", async () => {
    process.env.HOSTED_BACKEND_TARGET = "https://external.example.test";
    await expect(loadConfigFromFile({ command: "serve", mode: "test", isSsrBuild: false, isPreview: false }, configFile)).rejects.toThrow("HOSTED_BACKEND_TARGET");
  });

  it("serves styled auth and account deep links through CSP-safe dev and preview runtimes", async () => {
    const browser = findBrowser();
    expect(browser, "G2A-06 Hosted runtime smoke requires a system Chrome/Chromium or Edge executable in CI; install one before running npm test").toBeDefined();
    const backend = await startBackend();
    process.env.HOSTED_BACKEND_TARGET = backend.origin;
    delete process.env.HOSTED_DEV_TLS_PFX;
    delete process.env.HOSTED_DEV_TLS_PFX_PASSWORD;
    const vite = await createViteServer({
      configFile,
      root: appRoot,
      logLevel: "silent",
      server: { host: "127.0.0.1", port: 0, strictPort: false },
    });
    const outDir = createTemporaryRoot("hosted-preview-");
    let previewServer: Awaited<ReturnType<typeof preview>> | undefined;
    try {
      await vite.listen();
      const devOrigin = serverOrigin(vite.httpServer);
      await expectSecureHTML(devOrigin, true);
      await expectProxy(devOrigin, "dev", backend.requests);
      await expectSecureProxyResponses(devOrigin, "dev", backend.observedRequests);
      const devEvidence = await runBrowser(browser!, hostedURLs(devOrigin), createBrowserProfile("hosted-browser-dev-"), 30_000);
      expectStyledRuntime(devEvidence);
      expect(backend.requests.some((path) => path.includes("hint_auth_") && path.endsWith("/auth/bootstrap"))).toBe(true);
      expect(backend.requests.some((path) => path.includes("hint_account_") && path.endsWith("/account/bootstrap"))).toBe(true);
      await vite.close();

      await build({ configFile, root: appRoot, logLevel: "silent", build: { outDir, emptyOutDir: true } });
      const productionFiles = readdirSync(outDir, { recursive: true }).map(String);
      expect(productionFiles.filter((name) => name.endsWith(".map"))).toEqual([]);
      const productionJavaScript = productionFiles.filter((name) => name.endsWith(".js")).map((name) => readFileSync(join(outDir, name), "utf8")).join("\n");
      expect(productionJavaScript).not.toContain("sourceMappingURL");
      const productionHTML = readFileSync(join(outDir, "index.html"), "utf8");
      expect(productionHTML).toMatch(/<link rel="stylesheet"[^>]+href="\/assets\/.+\.css"/);
      expect(productionHTML).not.toContain("/src/styles.css");
      const productionCSS = readdirSync(join(outDir, "assets")).filter((name) => name.endsWith(".css")).map((name) => readFileSync(join(outDir, "assets", name), "utf8")).join("\n");
      expect(productionCSS).toContain(".client-button");
      expect(productionCSS).toContain(".client-field");
      expect(productionCSS).toContain(".account-block");
      expect(productionCSS.match(/\.client-button\{min-height/g)).toHaveLength(1);
      expect(productionCSS.match(/\.account-block\{width:min/g)).toHaveLength(1);
      previewServer = await preview({
        configFile,
        root: appRoot,
        logLevel: "silent",
        build: { outDir },
        preview: { host: "127.0.0.1", port: 0, strictPort: false },
      });
      const previewOrigin = serverOrigin(previewServer.httpServer);
      await expectSecureHTML(previewOrigin, false);
      await expectProxy(previewOrigin, "preview", backend.requests);
      await expectSecureProxyResponses(previewOrigin, "preview", backend.observedRequests);
      expectStyledRuntime(await runBrowser(browser!, hostedURLs(previewOrigin), createBrowserProfile("hosted-browser-preview-"), 30_000));
    } finally {
      await vite.close();
      if (previewServer) await closeServer(previewServer.httpServer);
      await closeServer(backend.server);
    }
  }, 180_000);

  it("renders G2B-04 entitlement account states in a real browser", async () => {
    const browser = findBrowser();
    expect(browser, "G2B-04 entitlement browser acceptance requires a system Chrome/Chromium or Edge executable").toBeDefined();
    const backend = await startG2B04EntitlementBackend();
    process.env.HOSTED_BACKEND_TARGET = backend.origin;
    delete process.env.HOSTED_DEV_TLS_PFX;
    delete process.env.HOSTED_DEV_TLS_PFX_PASSWORD;
    const vite = await createViteServer({
      configFile,
      root: appRoot,
      logLevel: "silent",
      server: { host: "127.0.0.1", port: 0, strictPort: false },
    });
    try {
      await vite.listen();
      const origin = serverOrigin(vite.httpServer);
      const result = await runBrowser(browser!, g2b04EntitlementURLs(origin), createBrowserProfile("hosted-browser-g2b04-"), 30_000);
      expect(result.consoleErrors).toEqual([]);
      expect(result.stderr).not.toMatch(/content security policy|refused to/i);
      const texts = Object.fromEntries(result.pages.map((page) => [new URL(page.href).searchParams.get("interaction_id"), page.rootText]));
      expect(texts[g2b04EntitlementID("entitled")]).toContain("权益摘要");
      expect(texts[g2b04EntitlementID("entitled")]).toContain("pro");
      expect(texts[g2b04EntitlementID("entitled")]).toContain("priority_queue");
      expect(texts[g2b04EntitlementID("entitled")]).not.toMatch(/¥|￥|paid|price|amount/i);
      expect(texts[g2b04EntitlementID("empty")]).toContain("当前没有可用权益");
      expect(texts[g2b04EntitlementID("expired")]).toContain("权益已到期");
      expect(texts[g2b04EntitlementID("expired")]).toContain("曾经拥有权益");
      expect(texts[g2b04EntitlementID("disabled")]).not.toContain("当前权益");
      expect(texts[g2b04EntitlementID("disabled")]).not.toContain("权益摘要");
      for (const id of Object.values(g2b04EntitlementIDs)) {
        expect(backend.requests.some((path) => path.includes(id) && path.endsWith("/account/bootstrap"))).toBe(true);
      }
    } finally {
      await vite.close();
      await closeServer(backend.server);
    }
  }, 120_000);

  it("creates isolated private browser runtime paths within the POSIX socket budget", () => {
    const first = createBrowserProfile("hosted-browser-first-");
    const second = createBrowserProfile("hosted-browser-second-");
    try {
      expect(first).not.toBe(second);
      if (process.platform === "win32") {
        expect(first.startsWith(tmpdir())).toBe(true);
        expect(browserEnvironment(first)).toBe(process.env);
      } else {
        expect(first).toMatch(/^\/tmp\/hbr-[^/]+$/);
        expect(Buffer.byteLength(join(first, "com.google.Chrome.XXXXXX", "SingletonSocket"), "utf8")).toBeLessThan(108);
        expect(browserEnvironment(first)).toMatchObject({ TMPDIR: first, TMP: first, TEMP: first });
      }
    } finally {
      releaseTemporaryRoot(first);
      releaseTemporaryRoot(second);
    }
  });

  it("fails immediately when the browser cannot start and removes its profile", async () => {
    const profile = createBrowserProfile("hosted-browser-startup-failure-");
    const missingBrowser = join(profile, "missing-browser");
    await expect(runBrowser(missingBrowser, ["http://127.0.0.1:1/ui/v1/auth?interaction_id=hint_auth_abcdefghijklmnopqrstuvwx"], profile, 1_000))
      .rejects.toThrow("browser startup failed before DevTools socket");
    expect(profileBrowserProcessIDs(profile)).toEqual([]);
    expect(existsSync(profile)).toBe(false);
  }, 10_000);

  it("restores the browser PID baseline and removes its profile after a failed probe", async () => {
    const browser = findBrowser();
    expect(browser, "G2A-06 Hosted runtime smoke requires a system Chrome/Chromium or Edge executable in CI; install one before running npm test").toBeDefined();
    const profile = createBrowserProfile("hosted-browser-failure-");
    await expect(runBrowser(browser!, ["http://127.0.0.1:1/ui/v1/auth?interaction_id=hint_auth_abcdefghijklmnopqrstuvwx"], profile, 1_000)).rejects.toThrow("browser page evidence timed out");
    expect(profileBrowserProcessIDs(profile)).toEqual([]);
    expect(existsSync(profile)).toBe(false);
  }, 90_000);
});

interface BackendFixture {
  readonly server: Server;
  readonly origin: string;
  readonly requests: string[];
  readonly observedRequests: BackendRequest[];
}

interface BackendRequest {
  readonly method: string;
  readonly path: string;
  readonly origin?: string;
}

async function startBackend(): Promise<BackendFixture> {
  const requests: string[] = [];
  const observedRequests: BackendRequest[] = [];
  const server = createHTTPServer((request, response) => {
    const path = request.url ?? "";
    requests.push(path);
    observedRequests.push({ method: request.method ?? "GET", path, origin: typeof request.headers.origin === "string" ? request.headers.origin : undefined });
    const secureProxyHeaders = {
      "Cache-Control": "no-store",
      "Content-Type": "application/json",
    };
    if (path.startsWith("/api/runtime-security/read")) {
      response.writeHead(202, secureProxyHeaders);
      response.end(JSON.stringify({ kind: "read", channel: new URL(path, "http://loopback").searchParams.get("channel") }));
      return;
    }
    if (path.startsWith("/api/v1/hosted/interactions/runtime-security-") && path.endsWith("/browser-session")) {
      response.writeHead(201, secureProxyHeaders);
      response.end(JSON.stringify({ kind: "write", channel: path.includes("preview") ? "preview" : "dev" }));
      return;
    }
    if (path.startsWith("/api/runtime-security/rejected")) {
      response.writeHead(403, secureProxyHeaders);
      response.end(JSON.stringify({ code: "hosted.csrf_failed", detail: "rejected" }));
      return;
    }
    if (request.url?.startsWith("/api/runtime-smoke")) {
      response.writeHead(200, { "Content-Type": "application/json", "X-Hosted-Backend": "loopback" });
      response.end(JSON.stringify({ proxied: true }));
      return;
    }
    const route = request.url?.includes("hint_account_") ? "hosted.account" : "hosted.auth";
    const interaction = hostedInteraction(route);
    response.writeHead(200, { "Content-Type": "application/json" });
    if (request.url?.endsWith("/browser-session")) {
      response.end(JSON.stringify({ interaction, csrf_token: "c".repeat(32), browser_session_expires_at: "2030-01-01T00:00:00Z" }));
    } else if (route === "hosted.account") {
      response.end(JSON.stringify({
        interaction,
        presentation: { product_name: "Runtime Account", theme_variant: null },
        profile: { user_id: "user_runtime", version: 1, display_name: "Runtime User", avatar_url: null, locale: "zh-CN", timezone: "Asia/Shanghai" },
        sessions: [{ session_id: "sess_runtime", current: true, device_label: "Runtime Browser", created_at: "2029-01-01T00:00:00Z", last_seen_at: "2029-01-01T00:00:00Z", expires_at: "2030-01-01T00:00:00Z" }],
        external_identities: [],
        allowed_actions: ["update_profile", "change_password", "revoke_session", "complete"],
      }));
    } else {
      response.end(JSON.stringify({ interaction, presentation: { product_name: "Runtime Auth", theme_variant: null }, flow: { kind: "login" }, password_enabled: true, registration_enabled: true, recovery_enabled: true, external_providers: [] }));
    }
  });
  await new Promise<void>((resolveListen, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolveListen);
  });
  return { server, origin: serverOrigin(server), requests, observedRequests };
}

const g2b04EntitlementIDs = {
  entitled: "hint_g2b04_entitled_abcdefghijk",
  empty: "hint_g2b04_empty_abcdefghijklmn",
  expired: "hint_g2b04_expired_abcdefghijkl",
  disabled: "hint_g2b04_disabled_abcdefghij",
} as const;

function g2b04EntitlementID(kind: keyof typeof g2b04EntitlementIDs): string {
  return g2b04EntitlementIDs[kind];
}

function g2b04EntitlementURLs(origin: string): readonly string[] {
  return Object.values(g2b04EntitlementIDs).map((id) => `${origin}/ui/v1/account?interaction_id=${id}`);
}

async function startG2B04EntitlementBackend(): Promise<BackendFixture> {
  const requests: string[] = [];
  const observedRequests: BackendRequest[] = [];
  const server = createHTTPServer((request, response) => {
    const path = request.url ?? "";
    requests.push(path);
    observedRequests.push({ method: request.method ?? "GET", path, origin: typeof request.headers.origin === "string" ? request.headers.origin : undefined });
    const id = /\/api\/v1\/hosted\/interactions\/([^/]+)/.exec(path)?.[1] ?? g2b04EntitlementID("entitled");
    const interaction = hostedInteractionWithID(id, "hosted.account");
    response.writeHead(200, { "Cache-Control": "no-store", "Content-Type": "application/json" });
    if (path.endsWith("/browser-session")) {
      response.end(JSON.stringify({ interaction, csrf_token: "c".repeat(32), browser_session_expires_at: "2030-01-01T00:00:00Z" }));
      return;
    }
    response.end(JSON.stringify(g2b04AccountBootstrap(id, interaction)));
  });
  await new Promise<void>((resolveListen, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolveListen);
  });
  return { server, origin: serverOrigin(server), requests, observedRequests };
}

function hostedInteraction(route: "hosted.auth" | "hosted.account") {
  return { interaction_id: route === "hosted.account" ? "hint_account_abcdefghijklmnopqrstuvwx" : "hint_auth_abcdefghijklmnopqrstuvwx", route_id: route, channel: "web", status: "opened", allowed_actions: ["authenticate", "complete", "cancel"], created_at: "2029-01-01T00:00:00Z", expires_at: "2030-01-01T00:00:00Z" };
}

function hostedInteractionWithID(id: string, route: "hosted.auth" | "hosted.account") {
  return { interaction_id: id, route_id: route, channel: "web", status: "opened", allowed_actions: ["complete"], created_at: "2029-01-01T00:00:00Z", expires_at: "2030-01-01T00:00:00Z" };
}

function g2b04AccountBootstrap(id: string, interaction: ReturnType<typeof hostedInteractionWithID>) {
  const value = {
    interaction,
    presentation: { product_name: "G2B04 Entitlement", theme_variant: null },
    profile: { user_id: "user_g2b04", version: 1, display_name: "G2B04 User", avatar_url: null, locale: "zh-CN", timezone: "Asia/Shanghai" },
    sessions: [{ session_id: "sess_g2b04", current: true, device_label: "Runtime Browser", created_at: "2029-01-01T00:00:00Z", last_seen_at: "2029-01-01T00:00:00Z", expires_at: "2030-01-01T00:00:00Z" }],
    external_identities: [],
    allowed_actions: ["complete"],
  };
  if (id === g2b04EntitlementID("disabled")) return value;
  if (id === g2b04EntitlementID("empty")) return { ...value, entitlement_summary: { revision: 8, plan_code: null, features: {}, valid_until: null, offline_grace_until: null, updated_at: "2026-07-23T00:00:00Z" } };
  if (id === g2b04EntitlementID("expired")) return { ...value, entitlement_summary: { revision: 9, plan_code: "trial", features: {}, valid_until: "2020-01-01T00:00:00Z", offline_grace_until: null, updated_at: "2026-07-23T00:00:00Z" } };
  return { ...value, entitlement_summary: { revision: 10, plan_code: "pro", features: { priority_queue: true, export_limit: 100 }, valid_until: "2030-01-01T00:00:00Z", offline_grace_until: "2030-01-02T00:00:00Z", updated_at: "2026-07-23T00:00:00Z" } };
}

async function expectProxy(origin: string, channel: string, requests: string[]): Promise<void> {
  const path = `/api/runtime-smoke?channel=${channel}`;
  const response = await fetch(`${origin}${path}`);
  expect(response.status).toBe(200);
  expect(response.headers.get("x-hosted-backend")).toBe("loopback");
  await expect(response.json()).resolves.toEqual({ proxied: true });
  expect(requests).toContain(path);
}

async function expectSecureProxyResponses(origin: string, channel: "dev" | "preview", observedRequests: readonly BackendRequest[]): Promise<void> {
  const readPath = `/api/runtime-security/read?channel=${channel}`;
  const readResponse = await fetch(`${origin}${readPath}`);
  expect(readResponse.status).toBe(202);
  expectSecureRuntimeHeaders(readResponse);
  await expect(readResponse.json()).resolves.toEqual({ kind: "read", channel });
  expect(observedRequests).toContainEqual({ method: "GET", path: readPath, origin: undefined });

  const writePath = `/api/v1/hosted/interactions/runtime-security-${channel}/browser-session`;
  const writeResponse = await fetch(`${origin}${writePath}`, { method: "POST", headers: { Origin: origin } });
  expect(writeResponse.status).toBe(201);
  expectSecureRuntimeHeaders(writeResponse);
  await expect(writeResponse.json()).resolves.toEqual({ kind: "write", channel });
  expect(observedRequests).toContainEqual({ method: "POST", path: writePath, origin });

  const rejectedPath = `/api/runtime-security/rejected?channel=${channel}`;
  const rejectedResponse = await fetch(`${origin}${rejectedPath}`, { method: "POST", headers: { Origin: origin } });
  expect(rejectedResponse.status).toBe(403);
  expectSecureRuntimeHeaders(rejectedResponse);
  await expect(rejectedResponse.json()).resolves.toEqual({ code: "hosted.csrf_failed", detail: "rejected" });
  expect(observedRequests).toContainEqual({ method: "POST", path: rejectedPath, origin });
}

function expectSecureRuntimeHeaders(response: Response): void {
  expect(response.headers.get("access-control-allow-origin")).toBeNull();
  expect(response.headers.get("access-control-allow-credentials")).toBeNull();
  expect(response.headers.get("cache-control")).toBe("no-store");
}

async function expectSecureHTML(origin: string, development: boolean): Promise<void> {
  const response = await fetch(hostedURLs(origin)[0], { headers: { Origin: origin } });
  expectSecureRuntimeHeaders(response);
  const html = await response.text();
  const csp = response.headers.get("content-security-policy") ?? "";
  expect(response.status).toBe(200);
  expect(csp).toContain("style-src 'self'");
  expect(csp).toContain("script-src 'self'");
  expect(csp).not.toContain("'unsafe-inline'");
  expect(csp).not.toContain("'unsafe-eval'");
  expect(html).not.toContain("injectIntoGlobalHook");
  expect(html).not.toContain("/@vite/client");
  if (development) {
    expect(html).toContain('property="csp-nonce" nonce="hosted-vite-dev"');
    expect(html).toMatch(/<link rel="stylesheet" href="\/src\/styles\.css"\s*\/>/);
    const css = await fetch(`${origin}/src/styles.css`, { headers: { Accept: "text/css,*/*;q=0.1" } });
    expect(css.status).toBe(200);
    expect(css.headers.get("content-type")).toContain("text/css");
    expect(await css.text()).toContain(".hosted-root");
  } else {
    expect(html).not.toContain("csp-nonce");
    expect(html).toMatch(/<link rel="stylesheet"[^>]+href="\/assets\/.+\.css"/);
  }
}

function hostedURLs(origin: string): readonly string[] {
  return [`${origin}/ui/v1/auth?interaction_id=hint_auth_abcdefghijklmnopqrstuvwx`, `${origin}/ui/v1/account?interaction_id=hint_account_abcdefghijklmnopqrstuvwx`];
}

interface BrowserPageEvidence {
  readonly href: string;
  readonly route: string;
  readonly stylesheetCount: number;
  readonly cssRuleCount: number;
  readonly externalStylesheetCount: number;
  readonly rootDisplay: string;
  readonly rootMinHeight: string;
  readonly rootRows: string;
  readonly bodyBackground: string;
  readonly headerDisplay: string;
  readonly headerMinHeight: string;
  readonly mainWidth: string;
  readonly buttonDisplay: string;
  readonly buttonMinHeight: string;
  readonly fieldDisplay: string | null;
  readonly accountBorderStyle: string;
  readonly accountBackground: string;
  readonly accountPadding: string;
  readonly rootText: string;
}

interface BrowserEvidence {
  readonly pages: readonly BrowserPageEvidence[];
  readonly consoleErrors: readonly string[];
  readonly stderr: string;
  readonly processBaselineRestored: boolean;
  readonly profileRemoved: boolean;
  readonly networkRequests: readonly string[];
}

function expectStyledRuntime(result: BrowserEvidence): void {
  expect(result.pages).toHaveLength(2);
  expect(result.pages.map((page) => page.route)).toEqual(["auth", "account"]);
  for (const page of result.pages) {
    expect(page.href).toContain(`/ui/v1/${page.route}`);
    expect(page.stylesheetCount).toBeGreaterThan(0);
    expect(page.cssRuleCount).toBeGreaterThan(20);
    expect(page.externalStylesheetCount).toBeGreaterThan(0);
    expect(page.rootDisplay).toBe("grid");
    expect(Number.parseFloat(page.rootMinHeight)).toBeGreaterThanOrEqual(700);
    expect(page.rootRows).not.toBe("none");
    expect(page.bodyBackground).toBe("rgb(244, 247, 246)");
    expect(page.headerDisplay).toBe("flex");
    expect(page.headerMinHeight).toBe("68px");
    expect(Number.parseFloat(page.mainWidth)).toBeGreaterThan(300);
    expect(Number.parseFloat(page.mainWidth)).toBeLessThanOrEqual(760);
    expect(["inline-flex", "flex"]).toContain(page.buttonDisplay);
    expect(Number.parseFloat(page.buttonMinHeight)).toBeGreaterThanOrEqual(40);
    expect(page.accountBorderStyle, JSON.stringify(result.pages)).toBe("solid");
    expect(page.accountBackground).toBe("rgb(255, 255, 255)");
    expect(Number.parseFloat(page.accountPadding)).toBeGreaterThanOrEqual(18);
  }
  expect(result.pages[0]?.fieldDisplay, JSON.stringify(result.pages[0])).toBe("grid");
  expect(result.consoleErrors).toEqual([]);
  expect(result.stderr).not.toMatch(/content security policy|refused to|can't detect preamble/i);
  expect(result.processBaselineRestored).toBe(true);
  expect(result.profileRemoved).toBe(true);
}

function serverOrigin(server: HttpServer | null): string {
  const address = server?.address();
  if (!address || typeof address === "string") throw new Error("runtime server did not bind a TCP port");
  return `http://127.0.0.1:${address.port}`;
}

async function closeServer(server: HttpServer): Promise<void> {
  if (!server.listening) return;
  await new Promise<void>((resolveClose, reject) => server.close((error) => error ? reject(error) : resolveClose()));
}

function createTemporaryRoot(prefix: string): string {
  const root = mkdtempSync(join(tmpdir(), prefix));
  temporaryRoots.push(root);
  return root;
}

function createBrowserProfile(prefix: string): string {
  const root = mkdtempSync(join(process.platform === "win32" ? tmpdir() : "/tmp", process.platform === "win32" ? prefix : "hbr-"));
  temporaryRoots.push(root);
  if (process.platform !== "win32") {
    const metadata = lstatSync(root);
    if (!metadata.isDirectory() || metadata.isSymbolicLink() || (metadata.mode & 0o077) !== 0) {
      releaseTemporaryRoot(root);
      throw new Error("Hosted browser profile must be a private regular directory");
    }
  }
  return root;
}

function browserEnvironment(profile: string): NodeJS.ProcessEnv {
  return process.platform === "win32"
    ? process.env
    : { ...process.env, TMPDIR: profile, TMP: profile, TEMP: profile };
}

function findBrowser(): string | undefined {
  const candidates = process.platform === "win32"
    ? [
        join(process.env.ProgramFiles ?? "C:\\Program Files", "Google/Chrome/Application/chrome.exe"),
        join(process.env["ProgramFiles(x86)"] ?? "C:\\Program Files (x86)", "Microsoft/Edge/Application/msedge.exe"),
        join(process.env.LOCALAPPDATA ?? "", "Google/Chrome/Application/chrome.exe"),
      ]
    : ["google-chrome", "google-chrome-stable", "chromium", "chromium-browser"];
  return candidates.find((candidate) => {
    if (candidate.includes("/") || candidate.includes("\\")) return existsSync(candidate);
    return spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0;
  });
}

function readBrowserSocket(profile: string, stderr: string): string | undefined {
  const activePortPath = join(profile, "DevToolsActivePort");
  if (existsSync(activePortPath)) {
    const [portValue, socketPath] = readFileSync(activePortPath, "utf8").trim().split(/\r?\n/);
    const port = Number(portValue);
    if (Number.isInteger(port) && port > 0 && port <= 65_535 && /^\/devtools\/browser\/[A-Za-z0-9-]+$/.test(socketPath ?? "")) {
      return `ws://127.0.0.1:${port}${socketPath}`;
    }
  }
  return /DevTools listening on (ws:\/\/127\.0\.0\.1:\d+\/devtools\/browser\/[A-Za-z0-9-]+)/.exec(stderr)?.[1];
}

function browserStartupFailure(browser: string, profile: string, reason: string, stderr: string): Error {
  const browserName = browser.split(/[\\/]/).at(-1) ?? "browser";
  let detail = stderr.slice(-1_000);
  for (const [value, replacement] of [[profile, "<profile>"], [process.cwd(), "<cwd>"], [tmpdir(), "<tmp>"]] as const) {
    for (const variant of new Set([value, value.replaceAll("\\", "/"), value.replaceAll("/", "\\")])) {
      const escaped = variant.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      detail = detail.replace(new RegExp(escaped, "gi"), replacement);
    }
  }
  detail = detail.replace(/\s+/g, " ").trim();
  return new Error(`browser startup failed before DevTools socket (${browserName}; ${reason})${detail ? `: ${detail}` : ""}`);
}

async function runBrowser(browser: string, urls: readonly string[], profile: string, pageTimeoutMilliseconds = 15_000): Promise<BrowserEvidence> {
  const baselineProfilePIDs = new Set(profileBrowserProcessIDs(profile));
  if (baselineProfilePIDs.size !== 0) throw new Error("browser profile PID baseline was not empty");
  const child = spawn(browser, [
    "--headless=new",
    "--disable-gpu",
    "--no-sandbox",
    "--disable-background-networking",
    "--disable-component-update",
    "--disable-extensions",
    "--disable-sync",
    "--no-first-run",
    "--enable-logging=stderr",
    "--remote-debugging-address=127.0.0.1",
    "--remote-debugging-port=0",
    `--user-data-dir=${profile}`,
    "about:blank",
  ], {
    windowsHide: true,
    detached: process.platform !== "win32",
    env: browserEnvironment(profile),
  });
  let stderr = "";
  child.stderr.setEncoding("utf8").on("data", (chunk) => { stderr += chunk; });
  const exit = new Promise<number | null>((resolveExit) => {
    child.once("error", () => resolveExit(null));
    child.once("exit", resolveExit);
  });
  const startupFailure = new Promise<never>((_, reject) => {
    child.once("error", (error) => reject(browserStartupFailure(browser, profile, (error as NodeJS.ErrnoException).code ?? error.name, stderr)));
    child.once("exit", (code, signal) => reject(browserStartupFailure(browser, profile, `exit=${code ?? "null"}, signal=${signal ?? "none"}`, stderr)));
  });
  try {
    const browserSocket = await Promise.race([
      waitForValue(() => readBrowserSocket(profile, stderr), 30_000, () => browserStartupFailure(browser, profile, "timeout=30000ms", stderr).message),
      startupFailure,
    ]);
    const debuggerOrigin = new URL(browserSocket).origin.replace("ws:", "http:");
    const pageSocket = await Promise.race([
      waitForValue(async () => {
        try {
          const response = await fetch(`${debuggerOrigin}/json/list`, { signal: AbortSignal.timeout(2_000) });
          if (!response.ok) return undefined;
          const targets = await response.json() as Array<{ readonly type?: string; readonly url?: string; readonly webSocketDebuggerUrl?: string }>;
          return targets.find((target) => target.type === "page")?.webSocketDebuggerUrl;
        } catch {
          return undefined;
        }
      }, 15_000, "browser page target timed out"),
      startupFailure,
    ]);
    const session = await CDPSession.connect(pageSocket);
    const pages: BrowserPageEvidence[] = [];
    try {
      await session.command("Runtime.enable");
      await session.command("Log.enable");
      await session.command("Network.enable");
      await session.command("Page.enable");
      await session.command("Emulation.setDeviceMetricsOverride", { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
      for (const url of urls) {
        await session.command("Page.navigate", { url });
        pages.push(await Promise.race([
          waitForValue(
            () => readPageEvidence(session, url),
            pageTimeoutMilliseconds,
            () => `browser page evidence timed out (${new URL(url).pathname}; requests=${session.requests.length}; errors=${session.errors.length})`,
          ),
          startupFailure,
        ]));
      }
    } finally {
      session.close();
    }
    await cdpCommand(browserSocket, "Browser.close").catch(() => undefined);
    const code = await Promise.race([exit, new Promise<null>((resolveWait) => setTimeout(() => resolveWait(null), 5_000))]);
    if (code === null) terminateProcessTree(child.pid);
    return { pages, consoleErrors: session.errors, stderr, processBaselineRestored: true, profileRemoved: true, networkRequests: session.requests };
  } finally {
    terminateProcessTree(child.pid);
    await Promise.race([exit, new Promise<null>((resolveWait) => setTimeout(() => resolveWait(null), 5_000))]);
    await terminateProfileProcesses(profile, baselineProfilePIDs);
    releaseTemporaryRoot(profile);
  }
}

async function readPageEvidence(session: CDPSession, expectedURL: string): Promise<BrowserPageEvidence | undefined> {
  const expression = `(async () => {
    const root = document.querySelector('.hosted-root.client-ui-root');
    const header = document.querySelector('.hosted-header');
    const main = document.querySelector('.hosted-main');
    const button = document.querySelector('.client-button');
    const field = document.querySelector('.client-field');
    const account = document.querySelector('.account-block');
    if (location.href !== ${JSON.stringify(expectedURL)} || !root || !header || !main || !button || !account) return undefined;
    const entitlementButton = document.querySelector('.hosted-entitlement-nav button');
    if (entitlementButton instanceof HTMLButtonElement) {
      entitlementButton.click();
      await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    }
    const sheets = Array.from(document.styleSheets);
    const cssRuleCount = sheets.reduce((count, sheet) => {
      try { return count + sheet.cssRules.length; } catch { return count; }
    }, 0);
    const rootStyle = getComputedStyle(root);
    const headerStyle = getComputedStyle(header);
    const buttonStyle = getComputedStyle(button);
    const accountStyle = getComputedStyle(account);
    return {
      href: location.href,
      route: location.pathname.split('/').at(-1),
      stylesheetCount: sheets.length,
      cssRuleCount,
      externalStylesheetCount: sheets.filter((sheet) => Boolean(sheet.href)).length,
      rootDisplay: rootStyle.display,
      rootMinHeight: rootStyle.minHeight,
      rootRows: rootStyle.gridTemplateRows,
      bodyBackground: getComputedStyle(document.body).backgroundColor,
      headerDisplay: headerStyle.display,
      headerMinHeight: headerStyle.minHeight,
      mainWidth: getComputedStyle(main).width,
      buttonDisplay: buttonStyle.display,
      buttonMinHeight: buttonStyle.minHeight,
      fieldDisplay: field ? getComputedStyle(field).display : null,
      accountBorderStyle: accountStyle.borderTopStyle,
      accountBackground: accountStyle.backgroundColor,
      accountPadding: accountStyle.paddingTop,
      rootText: root.textContent,
    };
  })()`;
  const result = await session.command<{ readonly result?: { readonly value?: BrowserPageEvidence } }>("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
  const value = result.result?.value;
  return value && value.cssRuleCount > 0 ? value : undefined;
}

async function waitForValue<T>(read: () => T | undefined | Promise<T | undefined>, timeoutMilliseconds: number, timeoutMessage: string | (() => string)): Promise<T> {
  const deadline = Date.now() + timeoutMilliseconds;
  do {
    const value = await read();
    if (value !== undefined) return value;
    await new Promise((resolveWait) => setTimeout(resolveWait, 100));
  } while (Date.now() < deadline);
  throw new Error(typeof timeoutMessage === "string" ? timeoutMessage : timeoutMessage());
}

class CDPSession {
  readonly errors: string[] = [];
  readonly requests: string[] = [];
  private nextID = 1;
  private readonly pending = new Map<number, { readonly resolve: (value: unknown) => void; readonly reject: (error: Error) => void; readonly timeout: ReturnType<typeof setTimeout> }>();

  private constructor(private readonly socket: WebSocket) {
    socket.addEventListener("message", (event) => this.receive(String(event.data)));
    socket.addEventListener("close", () => this.rejectPending(new Error("CDP page session closed")));
    socket.addEventListener("error", () => this.rejectPending(new Error("CDP page session failed")));
  }

  static async connect(socketURL: string): Promise<CDPSession> {
    const socket = new WebSocket(socketURL);
    await new Promise<void>((resolveOpen, reject) => {
      socket.addEventListener("open", () => resolveOpen(), { once: true });
      socket.addEventListener("error", () => reject(new Error("CDP page connection failed")), { once: true });
    });
    return new CDPSession(socket);
  }

  async command<T = unknown>(method: string, params?: object): Promise<T> {
    const id = this.nextID++;
    return await new Promise<T>((resolveCommand, reject) => {
      const timeout = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`CDP ${method} timed out`));
      }, 15_000);
      this.pending.set(id, { resolve: (value) => resolveCommand(value as T), reject, timeout });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }

  close(): void {
    this.socket.close();
  }

  private receive(raw: string): void {
    const message = JSON.parse(raw) as { readonly id?: number; readonly method?: string; readonly result?: unknown; readonly error?: { readonly message?: string }; readonly params?: Record<string, unknown> };
    if (message.id !== undefined) {
      const pending = this.pending.get(message.id);
      if (!pending) return;
      clearTimeout(pending.timeout);
      this.pending.delete(message.id);
      if (message.error) pending.reject(new Error(message.error.message ?? "CDP command failed"));
      else pending.resolve(message.result);
      return;
    }
    if (message.method === "Runtime.exceptionThrown") this.errors.push(JSON.stringify(message.params?.exceptionDetails ?? message.params));
    if (message.method === "Runtime.consoleAPICalled" && ["error", "assert"].includes(String(message.params?.type))) this.errors.push(JSON.stringify(message.params));
    if (message.method === "Log.entryAdded") {
      const entry = message.params?.entry as { readonly level?: string; readonly source?: string; readonly text?: string; readonly url?: string; readonly lineNumber?: number } | undefined;
      if (entry?.level === "error" || entry?.source === "security") this.errors.push(`${entry.source ?? "log"}: ${entry.text ?? "error"} (${entry.url ?? "unknown"}:${entry.lineNumber ?? 0})`);
    }
    if (message.method === "Inspector.targetCrashed") this.errors.push("browser target crashed");
    if (message.method === "Network.loadingFailed" && message.params?.canceled !== true) this.errors.push(`network failed: ${JSON.stringify(message.params)}`);
    if (message.method === "Network.requestWillBeSent") {
      const request = message.params?.request as { readonly url?: string } | undefined;
      if (request?.url) this.requests.push(request.url);
    }
  }

  private rejectPending(error: Error): void {
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timeout);
      pending.reject(error);
    }
    this.pending.clear();
  }
}

async function cdpCommand(socketURL: string, method: string, params?: object): Promise<unknown> {
  return await new Promise((resolveCommand, reject) => {
    const socket = new WebSocket(socketURL);
    const id = 1;
    const timeout = setTimeout(() => { socket.close(); reject(new Error(`CDP ${method} timed out`)); }, 5_000);
    socket.addEventListener("open", () => socket.send(JSON.stringify({ id, method, params })));
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(String(event.data)) as { readonly id?: number; readonly result?: unknown; readonly error?: { readonly message?: string } };
      if (message.id !== id) return;
      clearTimeout(timeout);
      socket.close();
      if (message.error) reject(new Error(message.error.message ?? `CDP ${method} failed`));
      else resolveCommand(message.result);
    });
    socket.addEventListener("error", () => {
      clearTimeout(timeout);
      reject(new Error(`CDP ${method} connection failed`));
    });
  });
}

function terminateProcessTree(processID: number | undefined): void {
  if (processID === undefined) return;
  if (process.platform === "win32") {
    const taskkill = join(process.env.SystemRoot ?? "C:\\Windows", "System32", "taskkill.exe");
    spawnSync(taskkill, ["/PID", String(processID), "/T", "/F"], { stdio: "ignore", windowsHide: true, timeout: 5_000 });
  } else {
    try { process.kill(-processID, "SIGKILL"); } catch {
      try { process.kill(processID, "SIGKILL"); } catch {}
    }
  }
}

function profileBrowserProcessIDs(profile: string): number[] {
  if (process.platform === "win32") {
    const escaped = profile.replace(/'/g, "''");
    const command = `$needle='${escaped}'; Get-CimInstance Win32_Process -Filter \"Name='chrome.exe' OR Name='msedge.exe'\" | Where-Object { $_.CommandLine -like ('*' + $needle + '*') } | ForEach-Object { $_.ProcessId }`;
    const result = spawnSync("powershell.exe", ["-NoProfile", "-Command", command], { encoding: "utf8", windowsHide: true, timeout: 30_000 });
    if (result.status !== 0) throw new Error(`failed to audit Hosted browser process profile (status=${result.status ?? "timeout"}; signal=${result.signal ?? "none"}; stderr=${result.stderr.slice(-200).replace(/\s+/g, " ").trim()})`);
    return result.stdout.split(/\s+/).filter(Boolean).map(Number).filter(Number.isInteger);
  }
  const result = spawnSync("ps", ["-eo", "pid=,args="], { encoding: "utf8", timeout: 10_000 });
  if (result.status !== 0) throw new Error("failed to audit Hosted browser process profile");
  return result.stdout.split(/\r?\n/).filter((line) => line.includes(`--user-data-dir=${profile}`)).map((line) => Number.parseInt(line.trim(), 10)).filter(Number.isInteger);
}

async function terminateProfileProcesses(profile: string, baseline: ReadonlySet<number>): Promise<void> {
  for (let attempt = 0; attempt < 120; attempt++) {
    const created = profileBrowserProcessIDs(profile).filter((processID) => !baseline.has(processID));
    if (created.length === 0) return;
    for (const processID of created) terminateProcessTree(processID);
    await new Promise((resolveWait) => setTimeout(resolveWait, 250));
  }
  const residual = profileBrowserProcessIDs(profile).filter((processID) => !baseline.has(processID));
  if (residual.length !== 0) throw new Error(`Hosted browser process baseline was not restored: ${residual.join(",")}`);
}

function releaseTemporaryRoot(root: string): void {
  const index = temporaryRoots.indexOf(root);
  if (index >= 0) temporaryRoots.splice(index, 1);
  rmSync(root, { recursive: true, force: true, maxRetries: 20, retryDelay: 250 });
  if (existsSync(root)) throw new Error("Hosted browser profile cleanup failed");
}
