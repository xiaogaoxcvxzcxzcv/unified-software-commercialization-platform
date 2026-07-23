[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('start', 'stop', 'restart', 'status')]
    [string]$Action = 'status',
    [ValidateRange(1024, 65535)]
    [int]$Port = 8080,
    [ValidatePattern('^[a-zA-Z][a-zA-Z0-9_]{0,62}$')]
    [string]$Database = 'platform_local',
    [ValidateRange(1, 900)]
    [int]$AccessTTLSeconds = 900
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$RepoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..\..')).TrimEnd('\')
$BackendRoot = Join-Path $RepoRoot 'platform\backend'
$RuntimeRoot = Join-Path $RepoRoot '.runtime'
$DatabasePasswordFile = Join-Path $RuntimeRoot 'postgres\test-password.txt'
$TokenPepperFile = Join-Path $RuntimeRoot 'admin-token-pepper.txt'
$TargetRoot = Join-Path $RuntimeRoot 'local-assembly-output'
$ArtifactRoot = Join-Path $RuntimeRoot 'local-assembly-artifacts'
$G2C02TargetRoot = Join-Path $TargetRoot 'g2c02-a'
$G2C02ArtifactRoot = Join-Path $ArtifactRoot 'g2c02-a'
$InstanceName = "backend-local-$Port"
$CurrentExecutable = Join-Path $RuntimeRoot "$InstanceName.exe"
$NextExecutable = Join-Path $RuntimeRoot "$InstanceName-next.exe"
$CurrentMigrationExecutable = Join-Path $RuntimeRoot "$InstanceName-migrate.exe"
$NextMigrationExecutable = Join-Path $RuntimeRoot "$InstanceName-migrate-next.exe"
$PidFile = Join-Path $RuntimeRoot "$InstanceName.pid"
$SettingsFile = Join-Path $RuntimeRoot "$InstanceName-settings.json"
$StdoutLog = Join-Path $RuntimeRoot "$InstanceName.stdout.log"
$StderrLog = Join-Path $RuntimeRoot "$InstanceName.stderr.log"

function Test-PathWithin {
    param([string]$Path, [string]$Root)
    $resolvedPath = [IO.Path]::GetFullPath($Path).TrimEnd('\')
    $resolvedRoot = [IO.Path]::GetFullPath($Root).TrimEnd('\')
    return $resolvedPath.StartsWith($resolvedRoot + '\', [StringComparison]::OrdinalIgnoreCase)
}

function Assert-RuntimeFile {
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "Required runtime file is missing: $Path"
    }
    if ([string]::IsNullOrWhiteSpace([IO.File]::ReadAllText($Path))) {
        throw "Required runtime file is empty: $Path"
    }
}

function Get-BackendListener {
    $processIds = @(
        & netstat.exe -ano -p tcp |
            ForEach-Object {
                if ($_ -match "^\s*TCP\s+\S+:$Port\s+\S+\s+LISTENING\s+(\d+)\s*$") {
                    [int]$Matches[1]
                }
            } |
            Sort-Object -Unique
    )
    if ($processIds.Count -eq 0) { return $null }
    if ($processIds.Count -ne 1) { throw "Port $Port has multiple listener processes." }
    return Get-Process -Id $processIds[0] -ErrorAction Stop
}

function Assert-ManagedBackendProcess {
    param([System.Diagnostics.Process]$Process)
    if ([string]::IsNullOrWhiteSpace($Process.Path) -or
        -not (Test-PathWithin -Path $Process.Path -Root $RuntimeRoot) -or
        -not ([IO.Path]::GetFileName($Process.Path).StartsWith('backend-', [StringComparison]::OrdinalIgnoreCase))) {
        throw "Refusing to stop PID $($Process.Id): port $Port is not owned by this repository runtime."
    }
}

function Stop-ManagedBackend {
    $listener = Get-BackendListener
    if ($null -eq $listener) {
        Remove-Item -LiteralPath $PidFile -Force -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath $SettingsFile -Force -ErrorAction SilentlyContinue
        return
    }
    Assert-ManagedBackendProcess -Process $listener
    Stop-Process -Id $listener.Id -Force
    if (-not $listener.WaitForExit(15000)) {
        throw "Backend PID $($listener.Id) did not stop within 15 seconds."
    }
    Remove-Item -LiteralPath $PidFile -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $SettingsFile -Force -ErrorAction SilentlyContinue
}

function Build-Backend {
    Remove-Item -LiteralPath $NextExecutable -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $NextMigrationExecutable -Force -ErrorAction SilentlyContinue
    $previousGoCache = $env:GOCACHE
    $previousGoModCache = $env:GOMODCACHE
    $previousGoTelemetry = $env:GOTELEMETRY
    try {
        $env:GOCACHE = Join-Path $RuntimeRoot 'go-build-cache'
        $env:GOMODCACHE = Join-Path $RuntimeRoot 'go-mod-cache'
        $env:GOTELEMETRY = 'off'
        Push-Location $BackendRoot
        try {
            & go build -o $NextExecutable ./cmd/server
            if ($LASTEXITCODE -ne 0) { throw 'Backend build failed.' }
            & go build -o $NextMigrationExecutable ./cmd/migrate
            if ($LASTEXITCODE -ne 0) { throw 'Migration build failed.' }
        }
        finally { Pop-Location }
    }
    finally {
        $env:GOCACHE = $previousGoCache
        $env:GOMODCACHE = $previousGoModCache
        $env:GOTELEMETRY = $previousGoTelemetry
    }
}

function Start-ManagedBackend {
    if ($null -ne (Get-BackendListener)) {
        throw "Port $Port is already in use."
    }
    Assert-RuntimeFile -Path $DatabasePasswordFile
    Assert-RuntimeFile -Path $TokenPepperFile
    New-Item -ItemType Directory -Force -Path $TargetRoot, $ArtifactRoot, $G2C02TargetRoot, $G2C02ArtifactRoot | Out-Null
    if (-not (Test-PathWithin -Path $TargetRoot -Root $RuntimeRoot) -or
        -not (Test-PathWithin -Path $ArtifactRoot -Root $RuntimeRoot) -or
        -not (Test-PathWithin -Path $G2C02TargetRoot -Root $RuntimeRoot) -or
        -not (Test-PathWithin -Path $G2C02ArtifactRoot -Root $RuntimeRoot)) {
        throw 'Assembly output directories must stay inside the repository runtime root.'
    }

    Move-Item -LiteralPath $NextExecutable -Destination $CurrentExecutable -Force
    Move-Item -LiteralPath $NextMigrationExecutable -Destination $CurrentMigrationExecutable -Force
    $password = ([IO.File]::ReadAllText($DatabasePasswordFile)).Trim()
    $pepper = ([IO.File]::ReadAllText($TokenPepperFile)).Trim()
    $encodedPassword = [Uri]::EscapeDataString($password)
    $normalizedTargetRoot = $TargetRoot.Replace('\', '/')
    $normalizedArtifactRoot = $ArtifactRoot.Replace('\', '/')
    $normalizedG2C02TargetRoot = $G2C02TargetRoot.Replace('\', '/')
    $normalizedG2C02ArtifactRoot = $G2C02ArtifactRoot.Replace('\', '/')
    $targets = ConvertTo-Json -InputObject @(
        @{
            ref = 'workspace.default'
            environment = 'development'
            display_name = 'Local development workspace'
            summary = 'Server-managed local source and evidence output'
            is_default = $true
            target_root = $normalizedTargetRoot
            artifact_root = $normalizedArtifactRoot
        },
        @{
            ref = 'workspace.g2c02.a'
            environment = 'test'
            display_name = 'G2C-02 software A workspace'
            summary = 'Server-managed G2C-02 acceptance output'
            is_default = $true
            target_root = $normalizedG2C02TargetRoot
            artifact_root = $normalizedG2C02ArtifactRoot
        }
    ) -Compress
    $variableNames = @(
        'PLATFORM_ENVIRONMENT', 'PLATFORM_HTTP_ADDRESS', 'PLATFORM_DATABASE_URL',
        'PLATFORM_ADMIN_TOKEN_PEPPER', 'PLATFORM_ADMIN_ALLOWED_ORIGINS',
        'PLATFORM_ADMIN_BEARER_ENABLED', 'PLATFORM_ADMIN_ACCESS_TTL',
        'PLATFORM_ASSEMBLY_OUTPUT_TARGETS'
    )
    $previousValues = @{}
    foreach ($name in $variableNames) {
        $previousValues[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
    }

    $process = $null
    try {
        $env:PLATFORM_ENVIRONMENT = 'local'
        $env:PLATFORM_HTTP_ADDRESS = "127.0.0.1:$Port"
        $env:PLATFORM_DATABASE_URL = "postgres://platform_test:$encodedPassword@127.0.0.1:15432/${Database}?sslmode=disable"
        $env:PLATFORM_ADMIN_TOKEN_PEPPER = $pepper
        $env:PLATFORM_ADMIN_ALLOWED_ORIGINS = 'https://127.0.0.1:5174'
        $env:PLATFORM_ADMIN_BEARER_ENABLED = 'false'
        $env:PLATFORM_ADMIN_ACCESS_TTL = "${AccessTTLSeconds}s"
        $env:PLATFORM_ASSEMBLY_OUTPUT_TARGETS = $targets
        & $CurrentMigrationExecutable up
        if ($LASTEXITCODE -ne 0) { throw 'Database migration failed.' }
        $process = Start-Process `
            -FilePath $CurrentExecutable `
            -WorkingDirectory $BackendRoot `
            -RedirectStandardOutput $StdoutLog `
            -RedirectStandardError $StderrLog `
            -WindowStyle Hidden `
            -PassThru
    }
    finally {
        foreach ($name in $variableNames) {
            [Environment]::SetEnvironmentVariable($name, $previousValues[$name], 'Process')
        }
        $password = $null
        $pepper = $null
        $encodedPassword = $null
    }

    [IO.File]::WriteAllText($PidFile, [string]$process.Id, [Text.Encoding]::ASCII)
    $deadline = [DateTime]::UtcNow.AddSeconds(20)
    $ready = $false
    do {
        Start-Sleep -Milliseconds 250
        try {
            $ready = (Invoke-WebRequest -UseBasicParsing "http://127.0.0.1:$Port/health/ready" -TimeoutSec 2).StatusCode -eq 200
        }
        catch { $ready = $false }
    } while (-not $ready -and -not $process.HasExited -and [DateTime]::UtcNow -lt $deadline)

    if (-not $ready) {
        if (-not $process.HasExited) { Stop-Process -Id $process.Id -Force }
        Remove-Item -LiteralPath $PidFile -Force -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath $SettingsFile -Force -ErrorAction SilentlyContinue
        throw "Backend failed to become ready. Inspect $StderrLog."
    }
    $settings = [ordered]@{
        pid = $process.Id
        host = '127.0.0.1'
        port = $Port
        database = $Database
        access_ttl_seconds = $AccessTTLSeconds
    } | ConvertTo-Json -Compress
    [IO.File]::WriteAllText($SettingsFile, $settings, [Text.UTF8Encoding]::new($false))
    Write-Output "running pid=$($process.Id) host=127.0.0.1 port=$Port database=$Database access_ttl_seconds=$AccessTTLSeconds"
}

switch ($Action) {
    'status' {
        $listener = Get-BackendListener
        if ($null -eq $listener) {
            Write-Output "stopped host=127.0.0.1 port=$Port database=$Database"
            exit 0
        }
        Assert-ManagedBackendProcess -Process $listener
        $ready = $false
        try {
            $ready = (Invoke-WebRequest -UseBasicParsing "http://127.0.0.1:$Port/health/ready" -TimeoutSec 2).StatusCode -eq 200
        }
        catch { $ready = $false }
        $accessTTL = 'unknown'
        if (Test-Path -LiteralPath $SettingsFile -PathType Leaf) {
            try {
                $settings = [IO.File]::ReadAllText($SettingsFile) | ConvertFrom-Json
                if ([int]$settings.pid -eq $listener.Id -and [int]$settings.port -eq $Port) {
                    $accessTTL = [string]$settings.access_ttl_seconds
                }
            }
            catch { $accessTTL = 'unknown' }
        }
        Write-Output "running pid=$($listener.Id) ready=$ready host=127.0.0.1 port=$Port database=$Database access_ttl_seconds=$accessTTL"
    }
    'stop' {
        Stop-ManagedBackend
        Write-Output "stopped host=127.0.0.1 port=$Port database=$Database"
    }
    'start' {
        Build-Backend
        Start-ManagedBackend
    }
    'restart' {
        Build-Backend
        Stop-ManagedBackend
        Start-ManagedBackend
    }
}
