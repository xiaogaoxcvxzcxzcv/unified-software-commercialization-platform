[CmdletBinding()]
param(
    [ValidateSet('Core', 'Full')]
    [string]$Mode = 'Core',

    [switch]$RequirePostgres,

    [string]$ReportPath = 'artifacts/reviews/F0-03/quality-gate-report.json'
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$RepoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd('\', '/')
$OriginalLocation = Get-Location
$Results = New-Object System.Collections.Generic.List[object]

function Invoke-Native {
    param(
        [Parameter(Mandatory = $true)][string]$Command,
        [Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments
    )

    $global:LASTEXITCODE = 0
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Command exited with code $LASTEXITCODE"
    }
}

function Get-RepoRelativePath {
    param([Parameter(Mandatory = $true)][string]$Path)

    $basePath = $RepoRoot.TrimEnd('\', '/')
    $targetPath = [System.IO.Path]::GetFullPath($Path)
    $comparison = [System.StringComparison]::Ordinal
    if ([System.IO.Path]::DirectorySeparatorChar -eq '\') {
        $comparison = [System.StringComparison]::OrdinalIgnoreCase
    }
    if ([string]::Equals($targetPath, $basePath, $comparison)) {
        return '.'
    }
    $prefix = $basePath + [System.IO.Path]::DirectorySeparatorChar
    if (-not $targetPath.StartsWith($prefix, $comparison)) {
        throw "path leaves repository root: $targetPath"
    }
    return $targetPath.Substring($prefix.Length)
}

function Test-IsWithinRepoPath {
    param([Parameter(Mandatory = $true)][string]$Path)

    try {
        [void](Get-RepoRelativePath -Path $Path)
        return $true
    }
    catch {
        return $false
    }
}

function Invoke-GateStep {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][scriptblock]$Action
    )

    Write-Host "`n==> $Name"
    $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
    $status = 'passed'
    $message = ''
    try {
        & $Action
    }
    catch {
        $status = 'failed'
        $message = 'step_failed; inspect the local or hosted command log'
        Write-Host "FAILED: $Name" -ForegroundColor Red
    }
    finally {
        $stopwatch.Stop()
        $Results.Add([pscustomobject]@{
            name        = $Name
            status      = $status
            duration_ms = $stopwatch.ElapsedMilliseconds
            message     = $message
        })
    }
}

function Test-GitCommitRevision {
    param([AllowNull()][string]$Revision)
    if ([string]::IsNullOrWhiteSpace($Revision) -or $Revision -notmatch '^[0-9a-fA-F]{40}$' -or $Revision -match '^0{40}$') {
        return $false
    }
    & git -C $RepoRoot cat-file -e "${Revision}^{commit}" 2>$null
    return $LASTEXITCODE -eq 0
}

function Test-GitWhitespace {
    $baseRevision = $env:QUALITY_GATE_BASE_REVISION
    $headRevision = $env:QUALITY_GATE_HEAD_REVISION
    $commonArguments = @('-C', $RepoRoot, '-c', 'core.safecrlf=false', '-c', 'core.autocrlf=false')
    if (Test-GitCommitRevision -Revision $headRevision) {
        if (Test-GitCommitRevision -Revision $baseRevision) {
            Invoke-Native -Command 'git' -Arguments ($commonArguments + @('diff', '--check', $baseRevision, $headRevision, '--'))
            return
        }
        & git -C $RepoRoot cat-file -e "${headRevision}^{commit}" 2>$null
        if ($LASTEXITCODE -eq 0) {
            Invoke-Native -Command 'git' -Arguments ($commonArguments + @('diff', '--check', "${headRevision}^", $headRevision, '--'))
            return
        }
        Invoke-Native -Command 'git' -Arguments ($commonArguments + @('show', '--check', '--format=', $headRevision))
        return
    }
    Invoke-Native -Command 'git' -Arguments ($commonArguments + @('diff', '--check'))
}

function Invoke-GoTestsWithPostgresEvidence {
    $output = @(& go test -count=1 ./... 2>&1)
    $exitCode = $LASTEXITCODE
    foreach ($line in $output) {
        Write-Host $line
    }
    if ($RequirePostgres -and ($output -match 'PostgreSQL integration test skipped')) {
        throw 'required PostgreSQL integration tests were skipped'
    }
    if ($exitCode -ne 0) {
        throw "go test exited with code $exitCode"
    }
    if ($RequirePostgres) {
        Write-Host 'PostgreSQL integration tests completed without the missing-database skip marker'
    }
}

function Test-IsExcludedPath {
    param([Parameter(Mandatory = $true)][string]$Path)

    $relative = Get-RepoRelativePath -Path $Path
    return $relative -match '(^|[\\/])(?:\.git|\.runtime|node_modules|dist|coverage|\.npm-cache|\.cache)([\\/]|$)'
}

function Test-IsTextFile {
    param([Parameter(Mandatory = $true)][System.IO.FileInfo]$File)

    $extensions = @{
        '.css' = $true; '.example' = $true; '.go' = $true; '.html' = $true
        '.js' = $true; '.json' = $true; '.jsx' = $true; '.md' = $true
        '.mjs' = $true; '.ps1' = $true; '.sql' = $true; '.svg' = $true
        '.toml' = $true; '.ts' = $true; '.tsx' = $true; '.txt' = $true
        '.yaml' = $true; '.yml' = $true
    }
    if ($extensions.ContainsKey($File.Extension.ToLowerInvariant())) {
        return $true
    }
    return $File.Name -in @('.gitignore', '.gitattributes', '.npmrc', 'go.mod', 'go.sum')
}

function Get-RepositoryTextFiles {
    $candidates = New-Object System.Collections.Generic.List[System.IO.FileInfo]
    foreach ($entry in @('AGENTS.md', '.gitignore', '.gitattributes', 'docs', 'platform', 'scripts', '.github')) {
        $path = Join-Path $RepoRoot $entry
        if (Test-Path -LiteralPath $path -PathType Leaf) {
            $candidates.Add((Get-Item -Force -LiteralPath $path))
            continue
        }
        if (-not (Test-Path -LiteralPath $path -PathType Container)) {
            continue
        }
        foreach ($file in Get-ChildItem -Force -Recurse -File -LiteralPath $path) {
            $candidates.Add($file)
        }
    }

    $seen = New-Object 'System.Collections.Generic.HashSet[string]' ([System.StringComparer]::OrdinalIgnoreCase)
    foreach ($file in $candidates) {
        if ((Test-IsExcludedPath -Path $file.FullName) -or -not (Test-IsTextFile -File $file)) {
            continue
        }
        if ($seen.Add($file.FullName)) {
            $file
        }
    }
}

function Test-StrictUtf8 {
    $decoder = New-Object System.Text.UTF8Encoding($false, $true)
    $errors = New-Object System.Collections.Generic.List[string]
    $count = 0
    foreach ($file in Get-RepositoryTextFiles) {
        $count++
        try {
            [void]$decoder.GetString([System.IO.File]::ReadAllBytes($file.FullName))
        }
        catch {
            $errors.Add((Get-RepoRelativePath -Path $file.FullName))
        }
    }
    if ($errors.Count -gt 0) {
        throw "invalid UTF-8 files: $($errors -join ', ')"
    }
    Write-Host "Strict UTF-8 valid: $count text files"
}

function Test-Migrations {
    $directory = Join-Path $RepoRoot 'platform/backend/migrations'
    $sqlFiles = @(Get-ChildItem -File -LiteralPath $directory -Filter '*.sql')
    if ($sqlFiles.Count -eq 0) {
        throw 'no migration files found'
    }
    $versions = @{}
    foreach ($file in $sqlFiles) {
        if ($file.Name -notmatch '^(?<version>\d{6})_(?<name>[a-z0-9]+(?:_[a-z0-9]+)*)\.(?<direction>up|down)\.sql$') {
            throw "invalid migration filename: $($file.Name)"
        }
        $version = $Matches.version
        $name = $Matches.name
        $direction = $Matches.direction
        if (-not $versions.ContainsKey($version)) {
            $versions[$version] = @{ name = $name; up = $false; down = $false }
        }
        if ($versions[$version].name -ne $name) {
            throw "migration $version uses different names for up/down files"
        }
        if ($versions[$version][$direction]) {
            throw "migration $version has duplicate $direction file"
        }
        $versions[$version][$direction] = $true
    }
    $ordered = @($versions.Keys | Sort-Object)
    for ($index = 0; $index -lt $ordered.Count; $index++) {
        $version = $ordered[$index]
        $expected = '{0:D6}' -f ($index + 1)
        if ($version -ne $expected) {
            throw "migration sequence gap: expected $expected, found $version"
        }
        if (-not $versions[$version].up -or -not $versions[$version].down) {
            throw "migration $version must have one up and one down file"
        }
    }
    Write-Host "Migration pairs valid: $($ordered.Count) versions"
}

function Get-MarkdownLinkTarget {
    param([Parameter(Mandatory = $true)][string]$RawTarget)

    $target = $RawTarget.Trim()
    if ($target.StartsWith('<') -and $target.Contains('>')) {
        return $target.Substring(1, $target.IndexOf('>') - 1)
    }
    if ($target -match '^(?<path>\S+)(?:\s+["''].*)?$') {
        return $Matches.path
    }
    return $target
}

function Test-DocumentationLinks {
    $errors = New-Object System.Collections.Generic.List[string]
    $markdownFiles = @(Get-RepositoryTextFiles | Where-Object { $_.Extension -eq '.md' })
    $linkPattern = [regex]'!?\[[^\]]*\]\((?<target>[^)]+)\)'
    foreach ($file in $markdownFiles) {
        $lineNumber = 0
        foreach ($line in [System.IO.File]::ReadLines($file.FullName)) {
            $lineNumber++
            foreach ($match in $linkPattern.Matches($line)) {
                $target = Get-MarkdownLinkTarget -RawTarget $match.Groups['target'].Value
                if ([string]::IsNullOrWhiteSpace($target) -or $target.StartsWith('#')) {
                    continue
                }
                if ($target -match '^[a-zA-Z][a-zA-Z0-9+.-]*:') {
                    continue
                }
                $fragmentIndex = $target.IndexOf('#')
                if ($fragmentIndex -ge 0) {
                    $target = $target.Substring(0, $fragmentIndex)
                }
                $queryIndex = $target.IndexOf('?')
                if ($queryIndex -ge 0) {
                    $target = $target.Substring(0, $queryIndex)
                }
                if ([string]::IsNullOrWhiteSpace($target)) {
                    continue
                }
                try {
                    $target = [System.Uri]::UnescapeDataString($target)
                    if ($target.StartsWith('/')) {
                        $resolved = [System.IO.Path]::GetFullPath((Join-Path $RepoRoot $target.TrimStart('/')))
                    }
                    else {
                        $resolved = [System.IO.Path]::GetFullPath((Join-Path $file.DirectoryName $target))
                    }
                    if (-not (Test-IsWithinRepoPath -Path $resolved)) {
                        throw 'link leaves repository root'
                    }
                    if (-not (Test-Path -LiteralPath $resolved)) {
                        throw 'target does not exist'
                    }
                }
                catch {
                    $relative = Get-RepoRelativePath -Path $file.FullName
                    $errors.Add("${relative}:$lineNumber -> $target ($($_.Exception.Message))")
                }
            }
        }
    }
    if ($errors.Count -gt 0) {
        $shown = @($errors | Select-Object -First 20)
        throw "invalid local documentation links:`n$($shown -join "`n")"
    }
    Write-Host "Local documentation links valid: $($markdownFiles.Count) Markdown files"
}

function Test-SecretPatterns {
    $patterns = @(
        @{ name = 'private-key'; regex = [regex]'-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----' },
        @{ name = 'aws-access-key'; regex = [regex]'AKIA[0-9A-Z]{16}' },
        @{ name = 'github-token'; regex = [regex]'gh[pousr]_[A-Za-z0-9]{30,}' },
        @{ name = 'openai-key'; regex = [regex]'sk-(?:proj-)?[A-Za-z0-9_-]{20,}' },
        @{ name = 'slack-token'; regex = [regex]'xox[baprs]-[A-Za-z0-9-]{20,}' },
        @{ name = 'stripe-live-key'; regex = [regex]'(?:sk|rk)_live_[A-Za-z0-9]{16,}' },
        @{ name = 'jwt'; regex = [regex]'eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}' },
        @{ name = 'credential-url'; regex = [regex]'(?i)(?:postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis)://[^/\s:@]+:[^@\s/]+@' }
    )
    $violations = New-Object System.Collections.Generic.List[string]
    foreach ($file in Get-RepositoryTextFiles) {
        $lineNumber = 0
        foreach ($line in [System.IO.File]::ReadLines($file.FullName)) {
            $lineNumber++
            foreach ($pattern in $patterns) {
                if (-not $pattern.regex.IsMatch($line)) {
                    continue
                }
                if ($pattern.name -eq 'credential-url' -and $line -match '(?i)(change-me|replace-|placeholder|example|dummy|\$\{|\$[A-Za-z_]|<[^>]+>)') {
                    continue
                }
                $relative = Get-RepoRelativePath -Path $file.FullName
                $violations.Add("${relative}:$lineNumber [$($pattern.name)]")
            }
            if ($line -cmatch '^\s*[A-Z][A-Z0-9_]*(?:PASSWORD|SECRET|TOKEN|API_KEY|PRIVATE_KEY)\s*=\s*(?<value>[^\s#]+)') {
                $value = $Matches.value
                if ($value -notmatch '(?i)^(?:true|false)$|change-me|replace-|placeholder|example|dummy|test|\$\{|^\$|^<') {
                    $relative = Get-RepoRelativePath -Path $file.FullName
                    $violations.Add("${relative}:$lineNumber [literal-secret-assignment]")
                }
            }
        }
    }
    if ($violations.Count -gt 0) {
        $shown = @($violations | Select-Object -First 20)
        throw "possible committed secrets (values suppressed):`n$($shown -join "`n")"
    }
    Write-Host 'Secret pattern scan passed (matched values are never reported)'
}

function Write-GateReport {
    $absoluteReportPath = $ReportPath
    if (-not [System.IO.Path]::IsPathRooted($absoluteReportPath)) {
        $absoluteReportPath = Join-Path $RepoRoot $absoluteReportPath
    }
    $absoluteReportPath = [System.IO.Path]::GetFullPath($absoluteReportPath)
    $reportDirectory = Split-Path -Parent $absoluteReportPath
    New-Item -ItemType Directory -Force -Path $reportDirectory | Out-Null
    $failed = @($Results | Where-Object { $_.status -eq 'failed' })
    $commit = 'unavailable'
    try {
        $candidate = (& git -C $RepoRoot rev-parse --verify HEAD 2>$null).Trim()
        if ($LASTEXITCODE -eq 0 -and $candidate -match '^[0-9a-f]{40}$') {
            $commit = $candidate
        }
    }
    catch {
        $commit = 'unavailable'
    }
    $report = [ordered]@{
        schema_version   = 1
        generated_at_utc = [DateTime]::UtcNow.ToString('o')
        mode             = $Mode
        require_postgres = [bool]$RequirePostgres
        git_commit       = $commit
        passed           = ($failed.Count -eq 0)
        steps            = [object[]]($Results | ForEach-Object { $_ })
    }
    $json = $report | ConvertTo-Json -Depth 6
    [System.IO.File]::WriteAllText($absoluteReportPath, $json + [Environment]::NewLine, (New-Object System.Text.UTF8Encoding($false)))
    Write-Host "`nQuality gate report: $absoluteReportPath"
}

try {
    Set-Location $RepoRoot

    Invoke-GateStep -Name 'Git whitespace check' -Action { Test-GitWhitespace }
    Invoke-GateStep -Name 'Strict UTF-8' -Action { Test-StrictUtf8 }
    Invoke-GateStep -Name 'Migration naming and pairing' -Action { Test-Migrations }
    Invoke-GateStep -Name 'Local documentation links' -Action { Test-DocumentationLinks }
    Invoke-GateStep -Name 'Secret pattern scan' -Action { Test-SecretPatterns }
    Invoke-GateStep -Name 'OpenAPI contract' -Action {
        Invoke-Native -Command 'node' -Arguments @((Join-Path $RepoRoot 'platform/contracts/openapi/validate.mjs'))
    }

    if ($Mode -eq 'Full') {
        $testTemp = Join-Path $RepoRoot '.runtime/test-temp'
        New-Item -ItemType Directory -Force -Path $testTemp | Out-Null
        $env:TEMP = $testTemp
        $env:TMP = $testTemp
        $env:TMPDIR = $testTemp

        Invoke-GateStep -Name 'PostgreSQL test environment' -Action {
            if ($RequirePostgres -and [string]::IsNullOrWhiteSpace($env:TEST_DATABASE_URL)) {
                throw 'TEST_DATABASE_URL is required for this quality gate run'
            }
            if ([string]::IsNullOrWhiteSpace($env:TEST_DATABASE_URL)) {
                Write-Host 'TEST_DATABASE_URL is not set; PostgreSQL tests may explicitly skip'
            }
            else {
                Write-Host 'TEST_DATABASE_URL is set; connection details are suppressed'
            }
        }
        Invoke-GateStep -Name 'Machine contract schemas' -Action {
            $runtime = Join-Path $RepoRoot '.runtime'
            New-Item -ItemType Directory -Force -Path $runtime | Out-Null
            $env:GOTELEMETRY = 'off'
            $env:GOCACHE = Join-Path $runtime 'go-build-cache'
            $env:GOMODCACHE = Join-Path $runtime 'go-mod-cache'
            Push-Location (Join-Path $RepoRoot 'platform/backend')
            try { Invoke-Native -Command 'go' -Arguments @('test', '-count=1', './internal/modules/assembly/machinecontract') } finally { Pop-Location }
        }
        Invoke-GateStep -Name 'Machine capability and template catalogs' -Action {
            $runtime = Join-Path $RepoRoot '.runtime'
            New-Item -ItemType Directory -Force -Path $runtime | Out-Null
            $env:GOTELEMETRY = 'off'
            $env:GOCACHE = Join-Path $runtime 'go-build-cache'
            $env:GOMODCACHE = Join-Path $runtime 'go-mod-cache'
            Push-Location (Join-Path $RepoRoot 'platform/backend')
            try { Invoke-Native -Command 'go' -Arguments @('test', '-count=1', './internal/modules/assembly/machinecatalog') } finally { Pop-Location }
        }
        Invoke-GateStep -Name 'Go test' -Action {
            $runtime = Join-Path $RepoRoot '.runtime'
            New-Item -ItemType Directory -Force -Path $runtime | Out-Null
            $env:GOTELEMETRY = 'off'
            $env:GOCACHE = Join-Path $runtime 'go-build-cache'
            $env:GOMODCACHE = Join-Path $runtime 'go-mod-cache'
            Push-Location (Join-Path $RepoRoot 'platform/backend')
            try { Invoke-GoTestsWithPostgresEvidence } finally { Pop-Location }
        }
        Invoke-GateStep -Name 'Go vet' -Action {
            Push-Location (Join-Path $RepoRoot 'platform/backend')
            try { Invoke-Native -Command 'go' -Arguments @('vet', './...') } finally { Pop-Location }
        }
        Invoke-GateStep -Name 'Client SDK tests' -Action {
            Push-Location (Join-Path $RepoRoot 'platform/sdk/typescript')
            try { Invoke-Native -Command 'npm' -Arguments @('test') } finally { Pop-Location }
        }
        Invoke-GateStep -Name 'Client SDK build' -Action {
            Push-Location (Join-Path $RepoRoot 'platform/sdk/typescript')
            try { Invoke-Native -Command 'npm' -Arguments @('run', 'build') } finally { Pop-Location }
        }
        Invoke-GateStep -Name 'Client UI tests' -Action {
            Push-Location (Join-Path $RepoRoot 'platform/client-ui')
            try { Invoke-Native -Command 'npm' -Arguments @('test') } finally { Pop-Location }
        }
        Invoke-GateStep -Name 'Client UI build' -Action {
            Push-Location (Join-Path $RepoRoot 'platform/client-ui')
            try { Invoke-Native -Command 'npm' -Arguments @('run', 'build') } finally { Pop-Location }
        }
        Invoke-GateStep -Name 'Standard-A template smoke' -Action {
            & (Join-Path $RepoRoot 'scripts/verify-standard-template.ps1') -RepoRoot $RepoRoot
            if ($LASTEXITCODE -ne 0) { throw 'standard-a template smoke failed' }
        }
        Invoke-GateStep -Name 'Admin Vitest' -Action {
            Push-Location (Join-Path $RepoRoot 'platform/admin-web')
            try { Invoke-Native -Command 'npm' -Arguments @('test') } finally { Pop-Location }
        }
        Invoke-GateStep -Name 'Admin production build' -Action {
            Push-Location (Join-Path $RepoRoot 'platform/admin-web')
            try { Invoke-Native -Command 'npm' -Arguments @('run', 'build') } finally { Pop-Location }
        }
    }
}
finally {
    try { Write-GateReport } finally { Set-Location $OriginalLocation }
}

$failures = @($Results | Where-Object { $_.status -eq 'failed' })
if ($failures.Count -gt 0) {
    throw "quality gate failed: $($failures.name -join ', ')"
}

Write-Host "`nQuality gate passed: mode=$Mode steps=$($Results.Count)" -ForegroundColor Green
