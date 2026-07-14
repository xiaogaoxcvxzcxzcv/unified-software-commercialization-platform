[CmdletBinding()]
param(
    [string]$RepoRoot = '',
    [switch]$KeepOutput
)

$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($RepoRoot)) {
    $RepoRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
}
$RepoRoot = (Resolve-Path -LiteralPath $RepoRoot).Path
$RuntimeRoot = Join-Path $RepoRoot '.runtime'
$SmokeRoot = Join-Path $RuntimeRoot ("g1-07-standard-a-smoke-{0}" -f $PID)
$PackageRoot = Join-Path $SmokeRoot 'packages'
$TemplateRoot = Join-Path $RepoRoot 'platform/experimental/templates/standard-a/0.1.0/template'
$AdminNpmCache = Join-Path $RepoRoot 'platform/admin-web/.npm-cache'
$NpmCache = Join-Path $RuntimeRoot 'npm-cache'
$Npm = if ($env:OS -eq 'Windows_NT') { 'npm.cmd' } else { 'npm' }
$Node = (Get-Command node -ErrorAction Stop).Source
$env:GOTELEMETRY = 'off'
$env:GOCACHE = Join-Path $RuntimeRoot 'go-build-cache'
$env:GOMODCACHE = Join-Path $RuntimeRoot 'go-mod-cache'

function Invoke-NativeCommand {
    param([string]$Command, [string[]]$Arguments, [string]$WorkingDirectory)
    Push-Location $WorkingDirectory
    try {
        & $Command @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "$Command failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }
}

function Test-PreviewStart {
    param([string]$ProjectRoot, [int]$Port)
    $arguments = @(
        (Join-Path $ProjectRoot 'node_modules/vite/bin/vite.js'),
        'preview', '--host', '127.0.0.1', '--port', [string]$Port, '--strictPort', '--configLoader', 'runner'
    )
    $processInfo = New-Object System.Diagnostics.ProcessStartInfo
    $processInfo.FileName = $Node
    $processInfo.Arguments = ($arguments -join ' ')
    $processInfo.WorkingDirectory = $ProjectRoot
    $processInfo.UseShellExecute = $false
    $processInfo.CreateNoWindow = $true
    $processInfo.WindowStyle = [System.Diagnostics.ProcessWindowStyle]::Hidden
    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $processInfo
    if (-not $process.Start()) {
        throw "template preview process did not start on port $Port"
    }
    try {
        $ready = $false
        for ($attempt = 0; $attempt -lt 30; $attempt++) {
            if ($process.HasExited) { break }
            try {
                $response = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$Port/" -TimeoutSec 2
                if ($response.StatusCode -eq 200 -and $response.Content -match '<div id="root"></div>') {
                    $ready = $true
                    break
                }
            }
            catch {
                Start-Sleep -Milliseconds 200
            }
        }
        if (-not $ready) {
            $detail = if ($process.HasExited) { "process exited with code $($process.ExitCode)" } else { 'HTTP endpoint did not become ready' }
            throw "template preview did not start on port ${Port}: $detail"
        }
    }
    finally {
        if (-not $process.HasExited) {
            $process.Kill()
            $process.WaitForExit()
        }
        $process.Dispose()
    }
}

New-Item -ItemType Directory -Force -Path $PackageRoot | Out-Null
if (-not (Test-Path -LiteralPath $NpmCache)) {
    if (-not (Test-Path -LiteralPath $AdminNpmCache)) {
        throw 'the admin npm cache must be populated before the offline template smoke test'
    }
    Copy-Item -LiteralPath $AdminNpmCache -Destination $NpmCache -Recurse
}
$removeSmoke = -not $KeepOutput
try {
    foreach ($packageDirectory in @('platform/sdk/typescript', 'platform/client-ui')) {
        $fullPackageDirectory = Join-Path $RepoRoot $packageDirectory
        if (-not (Test-Path -LiteralPath (Join-Path $fullPackageDirectory 'dist'))) {
            throw "$packageDirectory must be built before the template smoke test"
        }
        Invoke-NativeCommand -Command $Npm -Arguments @('pack', '--silent', '--cache', $NpmCache, '--pack-destination', $PackageRoot) -WorkingDirectory $fullPackageDirectory
    }

    $sdkPackage = Get-ChildItem -LiteralPath $PackageRoot -Filter '*client-sdk*.tgz' | Select-Object -First 1
    $uiPackage = Get-ChildItem -LiteralPath $PackageRoot -Filter '*client-ui*.tgz' | Select-Object -First 1
    if (-not $sdkPackage -or -not $uiPackage) {
        throw 'local SDK or Client UI package was not produced'
    }

    $targets = @(
        @{ Name = 'web'; Port = 5197 },
        @{ Name = 'desktop_webview'; Port = 5198 }
    )
    foreach ($target in $targets) {
        $projectRoot = Join-Path $SmokeRoot $target.Name
        Invoke-NativeCommand -Command 'go' -Arguments @(
            'run', './cmd/render-template-preview',
            '--repository-root', $RepoRoot,
            '--template-id', 'standard-a',
            '--template-version', '0.1.0',
            '--target', $target.Name,
            '--output', $projectRoot,
            '--product-name', '标准模板验证软件'
        ) -WorkingDirectory (Join-Path $RepoRoot 'platform/backend')

        $customTarget = Join-Path $projectRoot 'src/custom'
        New-Item -ItemType Directory -Force -Path $customTarget | Out-Null
        Copy-Item -LiteralPath (Join-Path $TemplateRoot 'src/custom/routes') -Destination $customTarget -Recurse
        Copy-Item -LiteralPath (Join-Path $TemplateRoot 'src/custom/workbench.css') -Destination $customTarget

        Invoke-NativeCommand -Command $Npm -Arguments @(
            'install', '--offline', '--ignore-scripts', '--no-audit', '--no-fund', '--cache', $NpmCache,
            $sdkPackage.FullName, $uiPackage.FullName
        ) -WorkingDirectory $projectRoot
        Invoke-NativeCommand -Command $Npm -Arguments @('test') -WorkingDirectory $projectRoot
        Invoke-NativeCommand -Command $Npm -Arguments @('run', 'build') -WorkingDirectory $projectRoot
        Test-PreviewStart -ProjectRoot $projectRoot -Port $target.Port
    }

    Write-Host "standard-a template smoke passed for web and desktop_webview"
    if ($KeepOutput) {
        Write-Host "kept smoke output: $SmokeRoot"
    }
}
finally {
    if ($removeSmoke -and (Test-Path -LiteralPath $SmokeRoot)) {
        $resolvedSmoke = (Resolve-Path -LiteralPath $SmokeRoot).Path
        $resolvedRuntime = (Resolve-Path -LiteralPath $RuntimeRoot).Path
        if (-not $resolvedSmoke.StartsWith($resolvedRuntime + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
            throw "refusing to remove smoke output outside .runtime: $resolvedSmoke"
        }
        Remove-Item -LiteralPath $resolvedSmoke -Recurse -Force
    }
}
