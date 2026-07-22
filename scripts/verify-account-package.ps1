[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$backendRoot = Join-Path $repositoryRoot "platform/backend"
$sdkRoot = Join-Path $repositoryRoot "platform/sdk/typescript"
$clientUiRoot = Join-Path $repositoryRoot "platform/client-ui"
$harnessRoot = Join-Path $repositoryRoot "platform/contract-tests/account-generated"
$runtimeBase = Join-Path $repositoryRoot ".runtime"
$runtimeParent = Join-Path $runtimeBase "G2A-07"
$runtimeLeaf = "account-generated-$([Guid]::NewGuid().ToString('N'))"
$runtimeRoot = Join-Path $runtimeParent $runtimeLeaf
$goCache = Join-Path $repositoryRoot ".runtime/go-build-cache"

function Assert-PlainDirectory([string] $Path) {
    $item = Get-Item -LiteralPath $Path -Force
    if (-not $item.PSIsContainer -or (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0)) {
        throw "unsafe runtime directory"
    }
}
function Protect-SensitiveOutput([string] $Value) {
    $safe = $Value
    $workspaceSlash = $repositoryRoot.Replace("\", "/")
    $userRoot = [Environment]::GetFolderPath([Environment+SpecialFolder]::UserProfile)
    $userRootSlash = $userRoot.Replace("\", "/")
    $safe = $safe.Replace($repositoryRoot, "<workspace>").Replace($workspaceSlash, "<workspace>")
    if ($userRoot) {
        $safe = $safe.Replace($userRoot, "<user>").Replace($userRootSlash, "<user>")
    }
    $safe = [regex]::Replace($safe, '(?i)\bBearer\s+\S+', "Bearer <redacted>")
    $safe = [regex]::Replace(
        $safe,
        '(?i)("(?:access[_-]?token|refresh[_-]?token|credential|password|proof|secret)"\s*:\s*)"[^"]*"',
        '$1"<redacted>"'
    )
    return [regex]::Replace(
        $safe,
        '(?i)\b(access[_-]?token|refresh[_-]?token|credential|password|proof|secret)\b\s*[:=]\s*[^,\s]+',
        '$1=<redacted>'
    )
}

function Assert-Sanitizer {
    $sentinels = @("sentinel-token", "sentinel-refresh", "sentinel-credential", "sentinel-password", "sentinel-proof", "sentinel-secret", "sentinel-bearer")
    $probe = '{"access_token":"sentinel-token","refresh-token":"sentinel-refresh","credential":"sentinel-credential","password":"sentinel-password","proof":"sentinel-proof","secret":"sentinel-secret"} Authorization: Bearer sentinel-bearer ' + $repositoryRoot
    $safe = Protect-SensitiveOutput $probe
    foreach ($sentinel in $sentinels) {
        if ($safe.Contains($sentinel)) {
            throw "sanitizer self-test failed"
        }
    }
    if ($safe.Contains($repositoryRoot)) {
        throw "sanitizer path self-test failed"
    }
}

function Invoke-SanitizedNative(
    [string] $WorkingDirectory,
    [string] $Executable,
    [string[]] $Arguments,
    [string] $FailureCode
) {
    Push-Location $WorkingDirectory
    try {
        $lines = @(& $Executable @Arguments 2>&1)
        $exitCode = $LASTEXITCODE
    }
    finally {
        Pop-Location
    }

    foreach ($line in $lines) {
        Write-Host (Protect-SensitiveOutput ([string] $line))
    }
    if ($exitCode -ne 0) {
        throw $FailureCode
    }
}


try {
    Assert-Sanitizer
    Assert-PlainDirectory $repositoryRoot
    if (-not (Test-Path -LiteralPath $runtimeBase)) {
        New-Item -ItemType Directory -Path $runtimeBase | Out-Null
    }
    Assert-PlainDirectory $runtimeBase
    if (-not (Test-Path -LiteralPath $runtimeParent)) {
        New-Item -ItemType Directory -Path $runtimeParent | Out-Null
    }
    Assert-PlainDirectory $runtimeParent
    if (-not (Test-Path -LiteralPath $goCache)) {
        New-Item -ItemType Directory -Path $goCache | Out-Null
    }
    Assert-PlainDirectory $goCache

    $env:GOCACHE = $goCache
    $env:G2A07_ACCOUNT_OUTPUT_ROOT = $runtimeRoot
    try {
        Invoke-SanitizedNative $backendRoot "go" @("test", "./internal/modules/assembly/generation", "-run", "^TestAccountPackage", "-count=1") "account package generation verification failed"
    }
    finally {
        Remove-Item Env:G2A07_ACCOUNT_OUTPUT_ROOT -ErrorAction SilentlyContinue
    }

    foreach ($name in @("package.json", "package-lock.json", "tsconfig.json", "tsconfig.build.json")) {
        Copy-Item -LiteralPath (Join-Path $harnessRoot $name) -Destination (Join-Path $runtimeRoot $name)
    }

    foreach ($name in @("package.json", "package-lock.json")) {
        $runtimePackagePath = Join-Path $runtimeRoot $name
        $runtimePackageText = Get-Content -LiteralPath $runtimePackagePath -Raw -Encoding UTF8
        $runtimePackageText = $runtimePackageText.Replace('file:../../sdk/typescript', 'file:../../../platform/sdk/typescript')
        $runtimePackageText = $runtimePackageText.Replace('file:../../client-ui', 'file:../../../platform/client-ui')
        $runtimePackageText = $runtimePackageText.Replace('"../../sdk/typescript"', '"../../../platform/sdk/typescript"')
        $runtimePackageText = $runtimePackageText.Replace('"../../client-ui"', '"../../../platform/client-ui"')
        [IO.File]::WriteAllText($runtimePackagePath, $runtimePackageText, (New-Object Text.UTF8Encoding($false)))
    }

    Invoke-SanitizedNative $repositoryRoot "npm" @("--prefix", $sdkRoot, "run", "build") "local Client SDK build failed"
    Invoke-SanitizedNative $repositoryRoot "npm" @("--prefix", $clientUiRoot, "run", "build") "local Client UI build failed"

    Invoke-SanitizedNative $runtimeRoot "npm" @("ci", "--offline", "--ignore-scripts", "--no-audit", "--no-fund") "account generated harness dependency installation failed"
    Invoke-SanitizedNative $runtimeRoot "npm" @("run", "typecheck") "account generated harness typecheck failed"
    Invoke-SanitizedNative $runtimeRoot "npm" @("test") "account generated harness tests failed"
    Invoke-SanitizedNative $runtimeRoot "npm" @("run", "build") "account generated harness build failed"

    Write-Host "G2A-07 account package verification passed (generated root: .runtime/G2A-07/$runtimeLeaf)."
}
catch {
    throw "G2A-07 account package verification failed; command output was sanitized."
}
