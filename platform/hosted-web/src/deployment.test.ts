import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const root = resolve(import.meta.dirname, "..");

describe("hosted web production deployment", () => {
  it("serves both deep links with fail-closed browser security headers", () => {
    const nginx = readFileSync(resolve(root, "nginx/default.conf.template"), "utf8");

    expect(nginx).toContain("location = /ui/v1/auth");
    expect(nginx).toContain("location = /ui/v1/account");
    expect(nginx).toContain('Cache-Control "no-store" always');
    expect(nginx).toContain("frame-ancestors 'none'");
    expect(nginx).toContain("script-src 'self'");
    expect(nginx).not.toContain("'unsafe-inline'");
    expect(nginx).toContain('X-Frame-Options "DENY" always');
    expect(nginx).toContain('Referrer-Policy "no-referrer" always');
    expect(nginx).toContain('X-Content-Type-Options "nosniff" always');
    expect(nginx).toContain("proxy_pass ${HOSTED_BACKEND_UPSTREAM}");
    expect(nginx).not.toMatch(/proxy_set_header\s+Authorization/i);
  });

  it("builds the client libraries before producing the immutable nginx image", () => {
    const dockerfile = readFileSync(resolve(root, "Dockerfile"), "utf8");

    expect(dockerfile).toContain("WORKDIR /workspace/platform/sdk/typescript");
    expect(dockerfile).toContain("WORKDIR /workspace/platform/client-ui");
    expect(dockerfile).toContain("WORKDIR /workspace/platform/hosted-web");
    expect(dockerfile).toContain("NGINX_ENVSUBST_FILTER=HOSTED_BACKEND_UPSTREAM");
    expect(dockerfile).toContain("COPY --from=build /workspace/platform/hosted-web/dist /usr/share/nginx/html");
  });

  it("starts HTTPS only from the existing controlled Admin PFX without exposing its passphrase", () => {
    const script = readFileSync(resolve(root, "scripts/start-dev-https.ps1"), "utf8");

    expect(script).toContain("admin-web.pfx");
    expect(script).toContain("admin-web-pfx-password.txt");
    expect(script).toContain("$CurrentUserSID.ToLowerInvariant() -replace '[^a-z0-9-]', '-'");
    expect(script).toContain("$TLSRoot = Join-Path $TLSBaseRoot ('user-' + $NormalizedUserSID)");
    expect(script).toContain("[IO.FileAttributes]::ReparsePoint");
    expect(script).toContain("Test-PathWithin");
    expect(script).toContain("$env:HOSTED_DEV_TLS_PFX = $PFX.Path");
    expect(script).toContain("$env:HOSTED_DEV_TLS_PFX_PASSWORD = $Passphrase");
    expect(script).toContain("$env:HOSTED_BACKEND_TARGET = 'http://127.0.0.1:8080'");
    expect(script).toContain("Start-Process -FilePath $npm.Source");
    expect(script).toContain("'--port', '5175', '--strictPort'");
    expect(script).toContain("[Environment]::SetEnvironmentVariable($Name, $PreviousValues[$Name], 'Process')");
    expect(script).toContain("$MaximumPFXBytes = 10MB");
    expect(script).toContain("$MaximumPasswordBytes = 4KB");
    expect(script).toContain("Assert-PrivateFileACL");
    expect(script).toContain("Assert-PrivateDirectoryACL");
    expect(script).toContain("Get-PfxData -FilePath $PFX.Path");
    expect(script).toContain("Assert-UnchangedFile");
    expect(script).toContain("Stop-ControlledProcessTree");
    expect(script).not.toMatch(/New-SelfSignedCertificate|Export-PfxCertificate|Remove-Item/i);
    expect(script).not.toMatch(/Write-(Host|Output).*Passphrase/i);
  });

  it("keeps hosted-only navigation and controls visible in forced-colors and dark themes", () => {
    const styles = readFileSync(resolve(root, "src/styles.css"), "utf8");

    expect(styles).toMatch(/\.hosted-skip\s*\{[^}]*min-height:\s*44px;[^}]*display:\s*inline-flex;[^}]*align-items:\s*center;/s);
    expect(styles).toMatch(/:root\[data-theme="dark"\][^{]*\{[^}]*--standard-brand:\s*#2dd4bf;[^}]*--standard-on-brand:\s*#062e2a;[^}]*--client-on-brand:\s*var\(--standard-on-brand\);/s);
    expect(styles).toContain("@media (forced-colors: active)");
    expect(styles).toContain("color: ButtonText");
    expect(styles).toContain("background: ButtonFace");
    expect(styles).toContain("outline: 3px solid Highlight");
  });
});
