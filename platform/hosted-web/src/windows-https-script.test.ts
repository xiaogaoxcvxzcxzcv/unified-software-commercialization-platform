// @vitest-environment node
import { afterEach, describe, expect, it } from "vitest";
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, symlinkSync, truncateSync, writeFileSync } from "node:fs";
import { request } from "node:https";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { spawn, spawnSync } from "node:child_process";

const windowsIt = process.platform === "win32" ? it : it.skip;
const appRoot = resolve(import.meta.dirname, "..");
const workspaceRoot = resolve(appRoot, "../..");
const sourceScript = resolve(appRoot, "scripts/start-dev-https.ps1");
const sourceAdminScript = resolve(workspaceRoot, "platform/admin-web/scripts/start-dev-https.ps1");
const currentSID = process.platform === "win32"
  ? spawnSync("powershell.exe", ["-NoProfile", "-Command", "[Security.Principal.WindowsIdentity]::GetCurrent().User.Value"], { encoding: "utf8", windowsHide: true }).stdout.trim()
  : "S-1-0-0";
const normalizedSID = currentSID.toLowerCase().replace(/[^a-z0-9-]/g, "-");
const temporaryRoots: string[] = [];

afterEach(() => {
  for (const root of temporaryRoots.splice(0)) rmSync(root, { recursive: true, force: true, maxRetries: 20, retryDelay: 250 });
});

describe("hosted Windows HTTPS launcher", () => {
  windowsIt("validates controlled regular files with private ACLs", () => {
    const sandbox = createSandbox();
    const result = runScript(sandbox, ["-ValidateOnly"]);
    expect(result.status).toBe(0);
    expect(result.stdout).toContain("hosted https inputs valid");
    expect(result.stdout).not.toContain(sandbox.passphrase);
    expect(sandbox.provisionOutput).not.toContain(sandbox.passphrase);
  }, 30_000);

  windowsIt.each([
    ["password over 4KiB", "password"],
    ["PFX over 10MiB", "pfx"],
  ])("rejects %s before reading or starting a child", (_name, kind) => {
    const sandbox = createSandbox();
    if (kind === "password") writeFileSync(sandbox.passwordPath, Buffer.alloc(4097, 0x61));
    else truncateSync(sandbox.pfxPath, 10 * 1024 * 1024 + 1);
    const result = runScript(sandbox, ["-ValidateOnly"]);
    expect(result.status).not.toBe(0);
    expect(`${result.stdout}${result.stderr}`).toContain("size is outside the allowed range");
    expect(`${result.stdout}${result.stderr}`).not.toContain(sandbox.passphrase);
  }, 30_000);

  windowsIt("rejects a reparse point in the complete TLS parent chain", () => {
    const root = createWorkspaceRoot();
    provisionAdminTLS(root);
    const tlsBase = join(root, ".runtime", "dev-tls");
    const redirectedTLSBase = join(root, ".runtime-real", "dev-tls");
    mkdirSync(dirname(redirectedTLSBase), { recursive: true });
    renameSync(tlsBase, redirectedTLSBase);
    symlinkSync(redirectedTLSBase, tlsBase, "junction");
    const result = runScript({ root }, ["-ValidateOnly"]);
    expect(result.status).not.toBe(0);
    expect(`${result.stdout}${result.stderr}`).toContain("reparse point");
  }, 30_000);

  windowsIt("leaves an existing 5175 listener untouched on port conflict", async () => {
    const sandbox = createSandbox();
    const listener = createServer();
    await new Promise<void>((resolveReady, reject) => listener.once("error", reject).listen(5175, "127.0.0.1", resolveReady));
    try {
      const result = runScript(sandbox, ["-SmokeTestSeconds", "1"]);
      expect(result.status).not.toBe(0);
      expect(`${result.stdout}${result.stderr}`).toContain("port 5175 is already in use");
      expect(listener.listening).toBe(true);
    } finally {
      await new Promise<void>((resolveClose) => listener.close(() => resolveClose()));
    }
  }, 30_000);

  windowsIt("does not start the HTTPS preview when the controlled production build fails", async () => {
    const sandbox = createSandbox({ fakeNPM: true, failBuild: true });
    const result = runScript(sandbox, ["-SmokeTestSeconds", "1"]);
    expect(result.status).not.toBe(0);
    expect(`${result.stdout}${result.stderr}`).toContain("production build failed");
    await expectPortAvailable();
  }, 30_000);

  windowsIt("runs a hidden HTTPS child, serves both deep links, restores state, redacts output, and releases 5175", async () => {
    const sandbox = createSandbox({ fakeNPM: true });
    const child = spawn("powershell.exe", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", sandbox.scriptPath!, "-SmokeTestSeconds", "3"], {
      cwd: dirname(sandbox.scriptPath!),
      env: { ...process.env, PATH: `${sandbox.binPath};${process.env.PATH ?? ""}`, HOSTED_DEV_TLS_PFX: "sentinel-pfx", HOSTED_DEV_TLS_PFX_PASSWORD: "sentinel-password", HOSTED_BACKEND_TARGET: "sentinel-target" },
      windowsHide: true,
    });
    let stdout = "";
    let stderr = "";
    let watchdogFired = false;
    const watchdog = setTimeout(() => {
      watchdogFired = true;
      void terminateProcessTree(child);
    }, 75_000);
    child.stdout.setEncoding("utf8").on("data", (chunk) => { stdout += chunk; });
    child.stderr.setEncoding("utf8").on("data", (chunk) => { stderr += chunk; });
    try {
      await waitForOutput(() => stdout.includes("hosted https ready"), child);

      for (const route of ["auth", "account"]) {
        const response = await getHTTPS(`https://127.0.0.1:5175/ui/v1/${route}?interaction_id=hint_abcdefghijklmnopqrstuvwxyz`);
        expect(response.status).toBe(200);
        expect(response.headers["cache-control"]).toBe("no-store");
        expect(response.headers["content-security-policy"]).toContain("frame-ancestors 'none'");
        expect(response.headers["content-security-policy"]).toContain("style-src 'self'");
        expect(response.headers["content-security-policy"]).not.toContain("nonce-");
        expect(response.body).toContain('id="root"');
        expect(response.body).toContain('rel="stylesheet"');
        expect(response.body).not.toMatch(/nonce-|\/@vite\/client/);
      }
      const stylesheet = await getHTTPS("https://127.0.0.1:5175/assets/hosted.css");
      expect(stylesheet.status).toBe(200);
      expect(stylesheet.body).toContain(".account-block");

      const exitCode = await waitForChildExit(child);
      if (watchdogFired) throw new Error("launcher exceeded the 75 second watchdog");
      expect(exitCode, stderr).toBe(0);
      expect(stdout).not.toContain(sandbox.passphrase);
      expect(stderr).not.toContain(sandbox.passphrase);
      const report = JSON.parse(readFileSync(join(sandbox.root, ".runtime/hosted-web-https-smoke-result.json"), "utf8").replace(/^\uFEFF/, ""));
      expect(report).toEqual({ environment_restored: true, port_released: true, location_restored: true });
    } finally {
      clearTimeout(watchdog);
      await terminateProcessTree(child);
      await expectPortAvailable(10_000);
    }
  }, 90_000);

  windowsIt("derives the current user directory from the normalized Windows SID", () => {
    const script = readFileSync(sourceScript, "utf8");
    expect(script).toContain("$CurrentUserSID.ToLowerInvariant() -replace '[^a-z0-9-]', '-'");
    expect(`user-${normalizedSID}`).not.toContain("\\");
  });
});

function createWorkspaceRoot(): string {
  const root = mkdtempSync(join(tmpdir(), "hosted-https-"));
  temporaryRoots.push(root);
  const scriptPath = join(root, "platform", "hosted-web", "scripts", "start-dev-https.ps1");
  const adminScriptPath = join(root, "platform", "admin-web", "scripts", "start-dev-https.ps1");
  mkdirSync(dirname(scriptPath), { recursive: true });
  mkdirSync(dirname(adminScriptPath), { recursive: true });
  copyFileSync(sourceScript, scriptPath);
  copyFileSync(sourceAdminScript, adminScriptPath);
  return root;
}

function createSandbox(options: { fakeNPM?: boolean; failBuild?: boolean } = {}) {
  const root = createWorkspaceRoot();
  const scriptPath = join(root, "platform", "hosted-web", "scripts", "start-dev-https.ps1");
  const tlsRoot = join(root, ".runtime", "dev-tls", `user-${normalizedSID}`);
  const pfxPath = join(tlsRoot, "admin-web.pfx");
  const passwordPath = join(tlsRoot, "admin-web-pfx-password.txt");
  const provisionOutput = provisionAdminTLS(root);
  const passphrase = readFileSync(passwordPath, "utf8").trim();
  const binPath = join(root, "bin");
  if (options.fakeNPM) createFakeNPM(binPath, options.failBuild === true);
  return { root, scriptPath, pfxPath, passwordPath, passphrase, binPath, provisionOutput };
}

function provisionAdminTLS(root: string): string {
  const adminScriptPath = join(root, "platform", "admin-web", "scripts", "start-dev-https.ps1");
  const result = spawnSync("powershell.exe", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", adminScriptPath, "-ProvisionTLSOnly"], {
    cwd: dirname(adminScriptPath),
    encoding: "utf8",
    windowsHide: true,
    timeout: 45_000,
  });
  if (result.status !== 0) throw new Error(`Admin TLS provisioning failed: ${result.stderr}`);
  return `${result.stdout}${result.stderr}`;
}

function createFakeNPM(binPath: string, failBuild: boolean): void {
  mkdirSync(binPath, { recursive: true });
  writeFileSync(join(binPath, "npm.cmd"), `@echo off\r\nif "%1"=="run" if "%2"=="build" exit /b ${failBuild ? 23 : 0}\r\nnode "%~dp0server.mjs" %*\r\n`);
  writeFileSync(join(binPath, "server.mjs"), `
import { readFileSync } from "node:fs";
import { createServer } from "node:https";
if (!process.argv.slice(2).includes("preview")) process.exit(31);
const headers = {
  "Cache-Control": "no-store",
  "Content-Security-Policy": "default-src 'self'; frame-ancestors 'none'; style-src 'self'; script-src 'self'",
  "X-Frame-Options": "DENY",
  "Referrer-Policy": "no-referrer",
  "X-Content-Type-Options": "nosniff"
};
const server = createServer({ pfx: readFileSync(process.env.HOSTED_DEV_TLS_PFX), passphrase: process.env.HOSTED_DEV_TLS_PFX_PASSWORD }, (request, response) => {
  if (request.url === "/assets/hosted.css") {
    response.writeHead(200, { ...headers, "Content-Type": "text/css" });
    response.end('.client-button{display:inline-flex}.client-field{display:grid}.account-block{display:block}');
    return;
  }
  response.writeHead(200, { ...headers, "Content-Type": "text/html" });
  response.end('<link rel="stylesheet" href="/assets/hosted.css"><div id="root"></div>');
});
server.listen(5175, "127.0.0.1");
`);
}

function runScript(sandbox: { root: string; scriptPath?: string; binPath?: string }, args: string[]) {
  const scriptPath = sandbox.scriptPath ?? join(sandbox.root, "platform", "hosted-web", "scripts", "start-dev-https.ps1");
  return spawnSync("powershell.exe", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath, ...args], {
    cwd: dirname(scriptPath),
    env: { ...process.env, PATH: sandbox.binPath ? `${sandbox.binPath};${process.env.PATH ?? ""}` : process.env.PATH },
    encoding: "utf8",
    windowsHide: true,
    timeout: 30_000,
  });
}

async function waitForOutput(predicate: () => boolean, child: ReturnType<typeof spawn>): Promise<void> {
  const deadline = Date.now() + 20_000;
  while (!predicate()) {
    if (child.exitCode !== null) throw new Error(`launcher exited early with ${child.exitCode}`);
    if (Date.now() >= deadline) throw new Error("launcher readiness timed out");
    await new Promise((resolveWait) => setTimeout(resolveWait, 50));
  }
}

function getHTTPS(url: string): Promise<{ status: number | undefined; headers: Record<string, string | string[] | undefined>; body: string }> {
  return new Promise((resolveResponse, reject) => {
    const req = request(url, { rejectUnauthorized: false }, (response) => {
      let body = "";
      response.setEncoding("utf8");
      response.on("data", (chunk) => { body += chunk; });
      response.on("end", () => resolveResponse({ status: response.statusCode, headers: response.headers, body }));
    });
    req.on("error", reject);
    req.end();
  });
}

function checkPortAvailable(): Promise<void> {
  return new Promise((resolveAvailable, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(5175, "127.0.0.1", () => server.close(() => resolveAvailable()));
  });
}

async function expectPortAvailable(timeout = 0): Promise<void> {
  const deadline = Date.now() + timeout;
  for (;;) {
    try {
      await checkPortAvailable();
      return;
    } catch (error) {
      if (Date.now() >= deadline) throw error;
      await new Promise((resolveWait) => setTimeout(resolveWait, 100));
    }
  }
}

function waitForChildExit(child: ReturnType<typeof spawn>): Promise<number | null> {
  if (child.exitCode !== null || child.signalCode !== null) return Promise.resolve(child.exitCode);
  return new Promise((resolveExit) => child.once("exit", resolveExit));
}

async function terminateProcessTree(child: ReturnType<typeof spawn>): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null || child.pid === undefined) return;
  const taskkill = join(process.env.SystemRoot ?? "C:\\Windows", "System32", "taskkill.exe");
  spawnSync(taskkill, ["/PID", String(child.pid), "/T", "/F"], { encoding: "utf8", windowsHide: true, timeout: 5_000 });
  await Promise.race([
    waitForChildExit(child),
    new Promise<void>((resolveWait) => setTimeout(resolveWait, 5_000)),
  ]);
}
