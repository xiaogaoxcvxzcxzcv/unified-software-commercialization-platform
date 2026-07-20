// @vitest-environment node
import { afterEach, describe, expect, it } from "vitest";
import { createHash } from "node:crypto";
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { spawnSync } from "node:child_process";

const windowsIt = process.platform === "win32" ? it : it.skip;
const appRoot = resolve(import.meta.dirname, "../..");
const sourceScript = resolve(appRoot, "scripts/start-dev-https.ps1");
const sourceHostedScript = resolve(appRoot, "../hosted-web/scripts/start-dev-https.ps1");
const currentSID = process.platform === "win32"
  ? spawnSync("powershell.exe", ["-NoProfile", "-Command", "[Security.Principal.WindowsIdentity]::GetCurrent().User.Value"], { encoding: "utf8", windowsHide: true }).stdout.trim()
  : "S-1-0-0";
const normalizedSID = currentSID.toLowerCase().replace(/[^a-z0-9-]/g, "-");
const temporaryRoots: string[] = [];

afterEach(() => {
  for (const root of temporaryRoots.splice(0)) rmSync(root, { recursive: true, force: true });
});

describe("admin Windows HTTPS TLS provisioning", () => {
  it("keeps the strict certificate validation algorithm identical in both launchers", () => {
    const adminBlock = certificateValidationBlock(readFileSync(sourceScript, "utf8"));
    const hostedBlock = certificateValidationBlock(readFileSync(sourceHostedScript, "utf8"));
    expect(createHash("sha256").update(adminBlock).digest("hex")).toBe(createHash("sha256").update(hostedBlock).digest("hex"));
    expect(adminBlock).toContain("$Certificate.HasPrivateKey");
    expect(adminBlock).toContain("1.3.6.1.5.5.7.3.1");
    expect(adminBlock).toContain("Assert-StrictLocalhostSAN -RawData");
    expect(adminBlock).not.toContain(".Format(");
  });

  windowsIt("accepts only strict DER GeneralNames for localhost and 127.0.0.1", () => {
    const valid = [0x30, 0x11, 0x82, 0x09, ...Buffer.from("localhost", "ascii"), 0x87, 0x04, 127, 0, 0, 1];
    expect(runSANValidator(valid).status).toBe(0);

    const evilDNS = [0x30, 0x16, 0x82, 0x0e, ...Buffer.from("localhost.evil", "ascii"), 0x87, 0x04, 127, 0, 0, 1];
    const invalidCases = [
      [0x30, 0x81, 0x11, ...valid.slice(2)],
      evilDNS,
      [...valid.slice(0, -1), 2],
      [...valid.slice(0, 2), 0x86, ...valid.slice(3)],
    ];
    for (const raw of invalidCases) {
      const result = runSANValidator(raw);
      expect(result.status).not.toBe(0);
      expect(`${result.stdout}${result.stderr}`).toContain("Development TLS SAN");
    }
  }, 30_000);

  windowsIt("rejects certificates without a private key or exact serverAuth EKU", () => {
    const sandbox = createSandbox();
    const provisioned = runScript(sandbox, ["-ProvisionTLSOnly"]);
    expect(provisioned.status, provisioned.stderr).toBe(0);
    const passphrase = readFileSync(sandbox.passwordPath, "utf8").trim();

    const publicOnly = runCertificateVariant(sandbox, "public");
    expect(publicOnly.status).not.toBe(0);
    expect(publicOnly.stdout).toContain("TLS_TEST_CERTIFICATE_VARIANT_READY");
    expect(`${publicOnly.stdout}${publicOnly.stderr}`).toContain("must contain a private key");
    expect(`${publicOnly.stdout}${publicOnly.stderr}`).not.toContain(passphrase);

    const clientOnly = runCertificateVariant(sandbox, "client-eku");
    expect(clientOnly.status).not.toBe(0);
    expect(clientOnly.stdout).toContain("TLS_TEST_CERTIFICATE_VARIANT_READY");
    expect(`${clientOnly.stdout}${clientOnly.stderr}`).toContain("EKU must contain serverAuth");
    expect(`${clientOnly.stdout}${clientOnly.stderr}`).not.toContain(passphrase);
  }, 60_000);

  windowsIt("creates a private pair without starting the requested port or exposing the passphrase", async () => {
    const sandbox = createSandbox();
    const listener = createServer();
    await new Promise<void>((resolveReady, reject) => listener.once("error", reject).listen(0, "127.0.0.1", resolveReady));
    const address = listener.address();
    if (address === null || typeof address === "string") throw new Error("test listener has no TCP port");
    try {
      const result = runScript(sandbox, ["-Port", String(address.port), "-ProvisionTLSOnly"]);
      expect(result.status, result.stderr).toBe(0);
      expect(result.stdout).toContain("admin https TLS provisioned");
      const passphrase = readFileSync(sandbox.passwordPath, "utf8").trim();
      expect(passphrase.length).toBeGreaterThan(20);
      expect(`${result.stdout}${result.stderr}`).not.toContain(passphrase);
      expect(listener.listening).toBe(true);
      expectPrivateACL(sandbox.tlsRoot);
      expectPrivateACL(sandbox.pfxPath);
      expectPrivateACL(sandbox.passwordPath);
    } finally {
      await new Promise<void>((resolveClose) => listener.close(() => resolveClose()));
    }
  }, 60_000);

  windowsIt("migrates an existing broad ACL without replacing either file", () => {
    const sandbox = createSandbox();
    const initial = runScript(sandbox, ["-ProvisionTLSOnly"]);
    expect(initial.status, initial.stderr).toBe(0);
    grantAuthenticatedUsersRead(sandbox.pfxPath);
    grantAuthenticatedUsersRead(sandbox.passwordPath);
    const pfxHash = sha256(sandbox.pfxPath);
    const passwordHash = sha256(sandbox.passwordPath);
    const passphrase = readFileSync(sandbox.passwordPath, "utf8").trim();

    const migrated = runScript(sandbox, ["-ProvisionTLSOnly"]);

    expect(migrated.status, migrated.stderr).toBe(0);
    expect(sha256(sandbox.pfxPath)).toBe(pfxHash);
    expect(sha256(sandbox.passwordPath)).toBe(passwordHash);
    expect(`${migrated.stdout}${migrated.stderr}`).not.toContain(passphrase);
    expectPrivateACL(sandbox.pfxPath);
    expectPrivateACL(sandbox.passwordPath);
  }, 60_000);

  windowsIt("rejects a reparse point in the complete TLS parent chain", () => {
    const sandbox = createSandbox({ createTLSDirectory: false });
    const redirectedTLS = join(sandbox.root, ".runtime-redirected", "dev-tls");
    mkdirSync(redirectedTLS, { recursive: true });
    symlinkSync(redirectedTLS, join(sandbox.root, ".runtime", "dev-tls"), "junction");

    const result = runScript(sandbox, ["-ProvisionTLSOnly"]);

    expect(result.status).not.toBe(0);
    expect(`${result.stdout}${result.stderr}`).toContain("reparse point");
  });

  windowsIt("uses exactly the same SID-isolated path as the Hosted consumer", () => {
    const sandbox = createSandbox({ includeHostedScript: true });
    const provisioned = runScript(sandbox, ["-ProvisionTLSOnly"]);
    expect(provisioned.status, provisioned.stderr).toBe(0);

    const validated = runHostedScript(sandbox, ["-ValidateOnly"]);

    expect(validated.status, validated.stderr).toBe(0);
    expect(validated.stdout).toContain("hosted https inputs valid");
    const passphrase = readFileSync(sandbox.passwordPath, "utf8").trim();
    expect(`${validated.stdout}${validated.stderr}`).not.toContain(passphrase);
  }, 60_000);

  windowsIt("ignores poisoned Security and Utility modules and imports trusted Windows PowerShell modules", () => {
    const sandbox = createSandbox({ includeHostedScript: true });
    const poisonRoot = join(sandbox.root, "poison-modules");
    for (const moduleName of ["Microsoft.PowerShell.Security", "Microsoft.PowerShell.Utility"]) {
      const poisonModule = join(poisonRoot, moduleName);
      mkdirSync(poisonModule, { recursive: true });
      writeFileSync(join(poisonModule, `${moduleName}.psd1`), `@{
RootModule = 'Missing-Module.psm1'
ModuleVersion = '99.0.0'
GUID = '3fd45f0a-0145-4a2d-9098-3d21f649ad01'
PowerShellVersion = '99.0'
}
`, "utf8");
    }
    const poisonedPath = `${poisonRoot};${process.env.PSModulePath ?? ""}`;

    const provisioned = runScript(sandbox, ["-ProvisionTLSOnly"], { PSModulePath: poisonedPath });
    expect(provisioned.status, provisioned.stderr).toBe(0);
    const passphrase = readFileSync(sandbox.passwordPath, "utf8").trim();
    expect(`${provisioned.stdout}${provisioned.stderr}`).not.toContain(passphrase);

    const validated = runHostedScript(sandbox, ["-ValidateOnly"], { PSModulePath: poisonedPath });
    expect(validated.status, validated.stderr).toBe(0);
    expect(validated.stdout).toContain("hosted https inputs valid");
    expect(`${validated.stdout}${validated.stderr}`).not.toContain(passphrase);
  }, 60_000);

  windowsIt("ignores legacy broad files and a different SID directory", () => {
    const sandbox = createSandbox();
    const legacyPFX = join(sandbox.root, ".runtime", "dev-tls", "admin-web.pfx");
    const legacyPassword = join(sandbox.root, ".runtime", "dev-tls", "admin-web-pfx-password.txt");
    const otherSIDRoot = join(sandbox.root, ".runtime", "dev-tls", "user-s-1-5-21-9999");
    const marker = "different-user-secret-marker";
    writeFileSync(legacyPFX, "legacy-invalid-pfx");
    writeFileSync(legacyPassword, "legacy-wide-password");
    grantAuthenticatedUsersRead(legacyPFX);
    grantAuthenticatedUsersRead(legacyPassword);
    mkdirSync(otherSIDRoot, { recursive: true });
    writeFileSync(join(otherSIDRoot, "admin-web-pfx-password.txt"), marker);
    const legacyPFXHash = sha256(legacyPFX);
    const legacyPasswordHash = sha256(legacyPassword);

    const result = runScript(sandbox, ["-ProvisionTLSOnly"]);

    expect(result.status, result.stderr).toBe(0);
    expect(sha256(legacyPFX)).toBe(legacyPFXHash);
    expect(sha256(legacyPassword)).toBe(legacyPasswordHash);
    expect(`${result.stdout}${result.stderr}`).not.toContain(marker);
    expectPrivateACL(sandbox.tlsRoot);
    expectPrivateACL(sandbox.pfxPath);
    expectPrivateACL(sandbox.passwordPath);
  }, 60_000);
});

function createSandbox(options: { createTLSDirectory?: boolean; includeHostedScript?: boolean } = {}) {
  const root = mkdtempSync(join(tmpdir(), "admin-tls-provision-"));
  temporaryRoots.push(root);
  const scripts = join(root, "platform", "admin-web", "scripts");
  const tlsBaseRoot = join(root, ".runtime", "dev-tls");
  const tlsRoot = join(tlsBaseRoot, `user-${normalizedSID}`);
  mkdirSync(scripts, { recursive: true });
  mkdirSync(join(root, ".runtime"), { recursive: true });
  if (options.createTLSDirectory !== false) {
    mkdirSync(tlsBaseRoot, { recursive: true });
    mkdirSync(tlsRoot, { recursive: true });
    setCurrentOwner(tlsRoot);
  }
  const scriptPath = join(scripts, "start-dev-https.ps1");
  copyFileSync(sourceScript, scriptPath);
  let hostedScriptPath: string | undefined;
  if (options.includeHostedScript) {
    hostedScriptPath = join(root, "platform", "hosted-web", "scripts", "start-dev-https.ps1");
    mkdirSync(dirname(hostedScriptPath), { recursive: true });
    copyFileSync(sourceHostedScript, hostedScriptPath);
  }
  return {
    root,
    scriptPath,
    hostedScriptPath,
    tlsRoot,
    pfxPath: join(tlsRoot, "admin-web.pfx"),
    passwordPath: join(tlsRoot, "admin-web-pfx-password.txt"),
  };
}

function setCurrentOwner(path: string) {
  const sidResult = spawnSync("powershell.exe", ["-NoProfile", "-Command", "[Security.Principal.WindowsIdentity]::GetCurrent().User.Value"], {
    encoding: "utf8",
    windowsHide: true,
  });
  expect(sidResult.status, `${sidResult.stdout}${sidResult.stderr}`).toBe(0);
  const sid = sidResult.stdout.trim();
  expect(sid).toMatch(/^S-1-/);
  const result = spawnSync("icacls.exe", [path, "/setowner", `*${sid}`, "/C"], {
    encoding: "utf8",
    windowsHide: true,
  });
  expect(result.status, `${result.stdout}${result.stderr}`).toBe(0);
}

function runScript(sandbox: ReturnType<typeof createSandbox>, args: string[], environment: Record<string, string> = {}) {
  return spawnSync("powershell.exe", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", sandbox.scriptPath, ...args], {
    cwd: dirname(sandbox.scriptPath),
    env: { ...process.env, ...environment },
    encoding: "utf8",
    windowsHide: true,
    timeout: 45_000,
  });
}

function runHostedScript(sandbox: ReturnType<typeof createSandbox>, args: string[], environment: Record<string, string> = {}) {
  if (!sandbox.hostedScriptPath) throw new Error("Hosted script was not installed in the sandbox");
  return spawnSync("powershell.exe", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", sandbox.hostedScriptPath, ...args], {
    cwd: dirname(sandbox.hostedScriptPath),
    env: { ...process.env, ...environment },
    encoding: "utf8",
    windowsHide: true,
    timeout: 45_000,
  });
}

function grantAuthenticatedUsersRead(path: string) {
  const result = spawnSync("icacls.exe", [path, "/grant", "*S-1-5-11:(R)"], {
    encoding: "utf8",
    windowsHide: true,
  });
  expect(result.status, `${result.stdout}${result.stderr}`).toBe(0);
}

function expectPrivateACL(path: string) {
  const script = `${trustedPowerShellPrelude()}
$acl = Get-Acl -LiteralPath $env:TLS_TEST_PATH -ErrorAction Stop
$owner = if ($acl.Owner.StartsWith('S-1-')) { (New-Object Security.Principal.SecurityIdentifier($acl.Owner)).Value } else { (New-Object Security.Principal.NTAccount($acl.Owner)).Translate([Security.Principal.SecurityIdentifier]).Value }
$entries = @($acl.Access | ForEach-Object { [pscustomobject]@{ sid = $_.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value; type = [string]$_.AccessControlType } })
[pscustomobject]@{ owner = $owner; current = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value; protected = $acl.AreAccessRulesProtected; entries = $entries } | ConvertTo-Json -Depth 4 -Compress
`;
  const result = spawnSync("powershell.exe", ["-NoProfile", "-Command", script], {
    env: { ...process.env, TLS_TEST_PATH: path },
    encoding: "utf8",
    windowsHide: true,
  });
  expect(result.status, result.stderr).toBe(0);
  expect(result.stderr).toBe("");
  const acl = JSON.parse(result.stdout.trim()) as {
    owner: string | null;
    current: string | null;
    protected: boolean | null;
    entries: Array<{ sid: string; type: string }> | null;
  };
  expect(acl.owner).not.toBeNull();
  expect(acl.current).not.toBeNull();
  expect(acl.protected).not.toBeNull();
  expect(acl.entries).not.toBeNull();
  expect(acl.owner).toBe(acl.current);
  expect(acl.protected).toBe(true);
  expect(new Set(acl.entries!.map((entry) => entry.sid))).toEqual(new Set([acl.current, "S-1-5-18"]));
  expect(acl.entries!.every((entry) => entry.type === "Allow")).toBe(true);
}

function sha256(path: string) {
  return createHash("sha256").update(readFileSync(path)).digest("hex");
}

function runSANValidator(raw: number[]) {
  const root = mkdtempSync(join(tmpdir(), "tls-san-validator-"));
  temporaryRoots.push(root);
  const scriptPath = join(root, "validate-san.ps1");
  writeFileSync(scriptPath, `${certificateValidationBlock(readFileSync(sourceScript, "utf8"))}\nAssert-StrictLocalhostSAN -RawData ([byte[]]@(${raw.join(",")}))\n`, "utf8");
  return spawnSync("powershell.exe", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath], {
    encoding: "utf8",
    windowsHide: true,
    timeout: 20_000,
  });
}

function runCertificateVariant(sandbox: ReturnType<typeof createSandbox>, variant: "public" | "client-eku") {
  const scriptPath = join(sandbox.root, `validate-${variant}.ps1`);
  const variantScript = variant === "public"
    ? String.raw`
$data = Get-PfxData -FilePath $env:TLS_TEST_PFX -Password $secure -ErrorAction Stop
$source = @($data.EndEntityCertificates)[0]
$cerPath = Join-Path $env:TLS_TEST_ROOT 'public.cer'
Export-Certificate -Cert $source -FilePath $cerPath -Force | Out-Null
$candidate = New-Object Security.Cryptography.X509Certificates.X509Certificate2($cerPath)
Write-Output 'TLS_TEST_CERTIFICATE_VARIANT_READY'
Assert-DevelopmentTLSCertificate -Certificate $candidate
`
    : String.raw`
$candidate = New-SelfSignedCertificate -Subject 'CN=localhost' -CertStoreLocation 'Cert:\CurrentUser\My' -KeyExportPolicy Exportable -TextExtension @('2.5.29.17={text}DNS=localhost&IPAddress=127.0.0.1','2.5.29.37={text}1.3.6.1.5.5.7.3.2')
Write-Output 'TLS_TEST_CERTIFICATE_VARIANT_READY'
try { Assert-DevelopmentTLSCertificate -Certificate $candidate } finally { Remove-Item -LiteralPath ('Cert:\CurrentUser\My\' + $candidate.Thumbprint) -Force -ErrorAction SilentlyContinue }
`;
  const script = `${trustedPowerShellPrelude()}
${certificateValidationBlock(readFileSync(sourceScript, "utf8"))}
$bytes = $null
$passphrase = $null
$secure = $null
try {
  $bytes = [IO.File]::ReadAllBytes($env:TLS_TEST_PASSWORD)
  $passphrase = [Text.Encoding]::UTF8.GetString($bytes).Trim()
  $secure = ConvertTo-SecureString -String $passphrase -AsPlainText -Force
${variantScript}
} finally {
  if ($null -ne $bytes) { [Array]::Clear($bytes, 0, $bytes.Length) }
  if ($null -ne $secure -and $secure -is [IDisposable]) { $secure.Dispose() }
  $passphrase = $null
}
`;
  writeFileSync(scriptPath, script, "utf8");
  return spawnSync("powershell.exe", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath], {
    env: { ...process.env, TLS_TEST_PFX: sandbox.pfxPath, TLS_TEST_PASSWORD: sandbox.passwordPath, TLS_TEST_ROOT: sandbox.root },
    encoding: "utf8",
    windowsHide: true,
    timeout: 30_000,
  });
}

function trustedPowerShellPrelude() {
  return String.raw`Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$trustedModuleRoot = Join-Path $PSHOME 'Modules'
$trustedModuleRootItem = Get-Item -LiteralPath $trustedModuleRoot -Force -ErrorAction Stop
if (-not $trustedModuleRootItem.PSIsContainer -or ($trustedModuleRootItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
  throw 'Windows PowerShell trusted module root must be a plain directory.'
}
foreach ($moduleManifestRelativePath in @(
  'Microsoft.PowerShell.Security\Microsoft.PowerShell.Security.psd1',
  'Microsoft.PowerShell.Utility\Microsoft.PowerShell.Utility.psd1',
  'PKI\PKI.psd1'
)) {
  $moduleManifest = Join-Path $trustedModuleRoot $moduleManifestRelativePath
  $moduleDirectoryItem = Get-Item -LiteralPath (Split-Path -Parent $moduleManifest) -Force -ErrorAction Stop
  $moduleManifestItem = Get-Item -LiteralPath $moduleManifest -Force -ErrorAction Stop
  if (-not $moduleDirectoryItem.PSIsContainer -or
      ($moduleDirectoryItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -or
      $moduleManifestItem.PSIsContainer -or
      ($moduleManifestItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
    throw 'Windows PowerShell trusted module input must not contain a reparse point.'
  }
  Import-Module -Name $moduleManifestItem.FullName -Force -ErrorAction Stop | Out-Null
}
`;
}

function certificateValidationBlock(script: string) {
  const match = script.match(/# TLS-CERTIFICATE-VALIDATION-BEGIN[\s\S]*?# TLS-CERTIFICATE-VALIDATION-END/);
  if (!match) throw new Error("TLS certificate validation block is missing");
  return match[0].replace(/\r\n/g, "\n");
}
