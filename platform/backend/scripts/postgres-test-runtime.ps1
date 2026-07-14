[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('start', 'stop', 'status')]
    [string]$Action = 'status'
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$Port = 15432
$ListenAddress = '127.0.0.1'
$DatabaseUser = 'platform_test'
$ControlDatabase = 'platform_test_control'
$RepoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..\..')).TrimEnd('\')
$RuntimeRoot = Join-Path $RepoRoot '.runtime\postgres'
$DriveMarker = Join-Path $RuntimeRoot 'subst-drive.txt'
$SubstExe = Join-Path $env:SystemRoot 'System32\subst.exe'

if (-not ('PostgresTestRuntime.NativeMethods' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Text;

namespace PostgresTestRuntime
{
    public static class NativeMethods
    {
        [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        public static extern uint QueryDosDevice(
            string lpDeviceName,
            StringBuilder lpTargetPath,
            int ucchMax);
    }
}
'@
}

function Get-DriveTarget {
    param([Parameter(Mandatory = $true)][string]$Drive)

    $buffer = New-Object System.Text.StringBuilder 32768
    $result = [PostgresTestRuntime.NativeMethods]::QueryDosDevice(
        $Drive,
        $buffer,
        $buffer.Capacity)

    if ($result -eq 0) {
        return $null
    }

    $target = $buffer.ToString().Split([char]0)[0]
    if ($target.StartsWith('\??\UNC\', [System.StringComparison]::OrdinalIgnoreCase)) {
        return '\\' + $target.Substring(8)
    }
    if ($target.StartsWith('\??\', [System.StringComparison]::OrdinalIgnoreCase)) {
        return $target.Substring(4)
    }

    return $target
}

function Test-SamePath {
    param(
        [Parameter(Mandatory = $true)][string]$Left,
        [Parameter(Mandatory = $true)][string]$Right
    )

    try {
        $normalizedLeft = [System.IO.Path]::GetFullPath($Left).TrimEnd('\')
        $normalizedRight = [System.IO.Path]::GetFullPath($Right).TrimEnd('\')
        return [string]::Equals(
            $normalizedLeft,
            $normalizedRight,
            [System.StringComparison]::OrdinalIgnoreCase)
    }
    catch {
        return $false
    }
}

function Get-MarkedDrive {
    if (-not (Test-Path -LiteralPath $DriveMarker -PathType Leaf)) {
        return $null
    }

    $drive = (Get-Content -Raw -Encoding ASCII -LiteralPath $DriveMarker).Trim().ToUpperInvariant()
    if ($drive -notmatch '^[A-Z]:$') {
        return $null
    }

    return $drive
}

function Set-MarkedDrive {
    param([Parameter(Mandatory = $true)][string]$Drive)

    Set-Content -Encoding ASCII -LiteralPath $DriveMarker -Value $Drive
}

function Clear-MarkedDrive {
    param([Parameter(Mandatory = $true)][string]$Drive)

    if (-not (Test-Path -LiteralPath $DriveMarker -PathType Leaf)) {
        return
    }

    if ((Get-MarkedDrive) -eq $Drive) {
        Remove-Item -Force -LiteralPath $DriveMarker
    }
}

function New-RepoDriveMapping {
    if (-not (Test-Path -LiteralPath $RuntimeRoot -PathType Container)) {
        throw "PostgreSQL runtime directory is missing: $RuntimeRoot"
    }

    $markedDrive = Get-MarkedDrive
    if ($null -ne $markedDrive) {
        $markedTarget = Get-DriveTarget -Drive $markedDrive
        if (($null -ne $markedTarget) -and (Test-SamePath -Left $markedTarget -Right $RepoRoot)) {
            return [pscustomobject]@{ Drive = $markedDrive; Created = $false }
        }
    }

    foreach ($code in 90..68) {
        $drive = ([char]$code).ToString() + ':'
        $target = Get-DriveTarget -Drive $drive
        if (($null -ne $target) -and (Test-SamePath -Left $target -Right $RepoRoot)) {
            return [pscustomobject]@{ Drive = $drive; Created = $false }
        }
    }

    foreach ($code in 82..68) {
        $drive = ([char]$code).ToString() + ':'
        if ($null -ne (Get-DriveTarget -Drive $drive)) {
            continue
        }

        & $SubstExe $drive $RepoRoot | Out-Null
        if ($LASTEXITCODE -ne 0) {
            continue
        }

        $target = Get-DriveTarget -Drive $drive
        if (($null -eq $target) -or (-not (Test-SamePath -Left $target -Right $RepoRoot))) {
            & $SubstExe $drive /D | Out-Null
            throw "The temporary ASCII drive mapping could not be verified."
        }

        return [pscustomobject]@{ Drive = $drive; Created = $true }
    }

    throw "No free drive letter is available for the temporary PostgreSQL runtime mapping."
}

function Remove-RepoDriveMapping {
    param([Parameter(Mandatory = $true)][string]$Drive)

    $target = Get-DriveTarget -Drive $Drive
    if ($null -eq $target) {
        Clear-MarkedDrive -Drive $Drive
        return
    }
    if (-not (Test-SamePath -Left $target -Right $RepoRoot)) {
        throw "Refusing to remove drive $Drive because it does not map to this repository."
    }

    & $SubstExe $Drive /D | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to remove the temporary drive mapping $Drive."
    }
    Clear-MarkedDrive -Drive $Drive
}

function Assert-Executable {
    param([Parameter(Mandatory = $true)][string]$Path)

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "Required PostgreSQL executable is missing: $Path"
    }
}

function Ensure-RuntimePasswordFile {
    param([Parameter(Mandatory = $true)][string]$PasswordFile)

    if (Test-Path -LiteralPath $PasswordFile -PathType Leaf) {
        if ([string]::IsNullOrWhiteSpace((Get-Content -Raw -LiteralPath $PasswordFile))) {
            throw "The runtime PostgreSQL password file is empty."
        }
        return
    }

    $bytes = New-Object byte[] 32
    $generator = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $generator.GetBytes($bytes)
        $password = ([System.BitConverter]::ToString($bytes)).Replace('-', '').ToLowerInvariant()
        Set-Content -NoNewline -Encoding ASCII -LiteralPath $PasswordFile -Value $password
    }
    finally {
        $password = $null
        $generator.Dispose()
    }
}

function Test-PostgresRunning {
    param(
        [Parameter(Mandatory = $true)][string]$PgCtl,
        [Parameter(Mandatory = $true)][string]$DataDir
    )

    if (-not (Test-Path -LiteralPath (Join-Path $DataDir 'PG_VERSION') -PathType Leaf)) {
        return $false
    }
    $pidPath = Join-Path $DataDir 'postmaster.pid'
    if (-not (Test-Path -LiteralPath $pidPath -PathType Leaf)) {
        return $false
    }
    $firstLine = Get-Content -LiteralPath $pidPath -TotalCount 1
    $serverProcessId = 0
    if (-not [int]::TryParse($firstLine, [ref]$serverProcessId)) {
        return $false
    }
    $serverProcess = Get-Process -Id $serverProcessId -ErrorAction SilentlyContinue
    return ($null -ne $serverProcess) -and ($serverProcess.ProcessName -eq 'postgres')
}

function Assert-ExpectedServerOptions {
    param([Parameter(Mandatory = $true)][string]$DataDir)

    $optionsPath = Join-Path $DataDir 'postmaster.opts'
    if (-not (Test-Path -LiteralPath $optionsPath -PathType Leaf)) {
        throw "The running PostgreSQL instance has no readable postmaster.opts file."
    }

    $options = Get-Content -Raw -Encoding UTF8 -LiteralPath $optionsPath
    $portPattern = '(?:^|\s)"?-p"?\s+"?15432"?(?:\s|$)'
    $hostPattern = '(?:^|\s)"?-h"?\s+"?127\.0\.0\.1"?(?:\s|$)'
    if (($options -notmatch $portPattern) -or ($options -notmatch $hostPattern)) {
        throw "Refusing to manage PostgreSQL because it is not bound to 127.0.0.1:15432."
    }
}

function Initialize-PostgresCluster {
    param(
        [Parameter(Mandatory = $true)][string]$InitDb,
        [Parameter(Mandatory = $true)][string]$DataDir,
        [Parameter(Mandatory = $true)][string]$PasswordFile
    )

    if (Test-Path -LiteralPath (Join-Path $DataDir 'PG_VERSION') -PathType Leaf) {
        return
    }

    if (-not (Test-Path -LiteralPath $PasswordFile -PathType Leaf)) {
        throw "The runtime PostgreSQL password file is missing."
    }
    if ([string]::IsNullOrWhiteSpace((Get-Content -Raw -LiteralPath $PasswordFile))) {
        throw "The runtime PostgreSQL password file is empty."
    }

    if (Test-Path -LiteralPath $DataDir -PathType Container) {
        $existing = @(Get-ChildItem -Force -LiteralPath $DataDir)
        if ($existing.Count -gt 0) {
            throw "The PostgreSQL data directory is not initialized and is not empty."
        }
    }
    else {
        New-Item -ItemType Directory -Path $DataDir | Out-Null
    }

    $ignored = @(& $InitDb `
        -D $DataDir `
        -U $DatabaseUser `
        '--auth-host=scram-sha-256' `
        '--auth-local=scram-sha-256' `
        "--pwfile=$PasswordFile" `
        '--encoding=UTF8' `
        '--locale=C' 2>&1)
    if ($LASTEXITCODE -ne 0) {
        throw "PostgreSQL cluster initialization failed."
    }
}

function Ensure-ControlDatabase {
    param(
        [Parameter(Mandatory = $true)][string]$Psql,
        [Parameter(Mandatory = $true)][string]$CreateDb,
        [Parameter(Mandatory = $true)][string]$PasswordFile
    )

    if (-not (Test-Path -LiteralPath $PasswordFile -PathType Leaf)) {
        throw "The runtime PostgreSQL password file is missing."
    }

    $password = (Get-Content -Raw -LiteralPath $PasswordFile).Trim()
    if ([string]::IsNullOrWhiteSpace($password)) {
        throw "The runtime PostgreSQL password file is empty."
    }

    $hadPassword = Test-Path Env:PGPASSWORD
    $previousPassword = $null
    if ($hadPassword) {
        $previousPassword = $env:PGPASSWORD
    }

    try {
        $env:PGPASSWORD = $password
        $query = "SELECT 1 FROM pg_database WHERE datname = '$ControlDatabase'"
        $result = @(& $Psql `
            -X -q -A -t `
            -v 'ON_ERROR_STOP=1' `
            -h $ListenAddress `
            -p $Port `
            -U $DatabaseUser `
            -d postgres `
            -c $query 2>&1)
        if ($LASTEXITCODE -ne 0) {
            throw "Unable to query the PostgreSQL control database."
        }

        if (($result -join '').Trim() -ne '1') {
            $ignored = @(& $CreateDb `
                -h $ListenAddress `
                -p $Port `
                -U $DatabaseUser `
                $ControlDatabase 2>&1)
            if ($LASTEXITCODE -ne 0) {
                throw "Unable to create the PostgreSQL control database."
            }
        }
    }
    finally {
        $password = $null
        if ($hadPassword) {
            $env:PGPASSWORD = $previousPassword
        }
        else {
            Remove-Item Env:PGPASSWORD -ErrorAction SilentlyContinue
        }
    }
}

$actualPgHome = Join-Path $RuntimeRoot 'pgsql'
$actualDataDir = Join-Path $RuntimeRoot 'data'
$actualPasswordFile = Join-Path $RuntimeRoot 'test-password.txt'
$actualPgCtl = Join-Path $actualPgHome 'bin\pg_ctl.exe'
$actualPsql = Join-Path $actualPgHome 'bin\psql.exe'
$actualCreateDb = Join-Path $actualPgHome 'bin\createdb.exe'

# A subst mapping can be process-local in managed execution environments. An
# initialized cluster can always be inspected and stopped through its physical
# path, so lifecycle checks must not depend on the temporary ASCII alias.
if ($Action -eq 'status') {
    Assert-Executable -Path $actualPgCtl
    if (Test-PostgresRunning -PgCtl $actualPgCtl -DataDir $actualDataDir) {
        Assert-ExpectedServerOptions -DataDir $actualDataDir
        Write-Output "running host=$ListenAddress port=$Port database=$ControlDatabase"
    }
    else {
        Write-Output "stopped host=$ListenAddress port=$Port database=$ControlDatabase"
    }
    exit 0
}

if ($Action -eq 'stop') {
    Assert-Executable -Path $actualPgCtl
    if (Test-PostgresRunning -PgCtl $actualPgCtl -DataDir $actualDataDir) {
        Assert-ExpectedServerOptions -DataDir $actualDataDir
        $ignored = @(& $actualPgCtl -D $actualDataDir -t 60 -w stop -m fast 2>&1)
        if ($LASTEXITCODE -ne 0) {
            throw "PostgreSQL failed to stop cleanly."
        }
    }
    $markedDrive = Get-MarkedDrive
    if ($null -ne $markedDrive) {
        Remove-RepoDriveMapping -Drive $markedDrive
    }
    Write-Output "stopped host=$ListenAddress port=$Port database=$ControlDatabase"
    exit 0
}

if ((Test-Path -LiteralPath $actualPgCtl -PathType Leaf) -and (Test-PostgresRunning -PgCtl $actualPgCtl -DataDir $actualDataDir)) {
    Assert-ExpectedServerOptions -DataDir $actualDataDir
    Assert-Executable -Path $actualPsql
    Assert-Executable -Path $actualCreateDb
    Ensure-ControlDatabase -Psql $actualPsql -CreateDb $actualCreateDb -PasswordFile $actualPasswordFile
    Write-Output "running host=$ListenAddress port=$Port database=$ControlDatabase"
    exit 0
}

$mapping = $null
$keepMapping = $false
$mappingRemoved = $false

try {
    $mapping = New-RepoDriveMapping
    $driveRoot = $mapping.Drive + '\'
    $mappedRuntime = Join-Path $driveRoot '.runtime\postgres'
    $pgHome = Join-Path $mappedRuntime 'pgsql'
    $dataDir = Join-Path $mappedRuntime 'data'
    $passwordFile = Join-Path $mappedRuntime 'test-password.txt'
    $logFile = Join-Path $mappedRuntime 'postgres.log'
    $initDb = Join-Path $pgHome 'bin\initdb.exe'
    $pgCtl = Join-Path $pgHome 'bin\pg_ctl.exe'
    $pgIsReady = Join-Path $pgHome 'bin\pg_isready.exe'
    $psql = Join-Path $pgHome 'bin\psql.exe'
    $createDb = Join-Path $pgHome 'bin\createdb.exe'

    Assert-Executable -Path $pgCtl

    switch ($Action) {
        'status' {
            if (Test-PostgresRunning -PgCtl $pgCtl -DataDir $dataDir) {
                Assert-ExpectedServerOptions -DataDir $dataDir
                Set-MarkedDrive -Drive $mapping.Drive
                $keepMapping = $true
                Write-Output "running host=$ListenAddress port=$Port database=$ControlDatabase"
            }
            else {
                Write-Output "stopped host=$ListenAddress port=$Port database=$ControlDatabase"
            }
        }

        'start' {
            Assert-Executable -Path $initDb
            Assert-Executable -Path $pgIsReady
            Assert-Executable -Path $psql
            Assert-Executable -Path $createDb

            Ensure-RuntimePasswordFile -PasswordFile $passwordFile

            Initialize-PostgresCluster `
                -InitDb $initDb `
                -DataDir $dataDir `
                -PasswordFile $passwordFile

            if (Test-PostgresRunning -PgCtl $pgCtl -DataDir $dataDir) {
                Assert-ExpectedServerOptions -DataDir $dataDir
            }
            else {
                $serverOptions = "-p $Port -h $ListenAddress"
                $startArguments = @(
                    '-D', $dataDir,
                    '-l', $logFile,
                    '-o', ('"' + $serverOptions + '"'),
                    '-t', '60',
                    '-w', 'start'
                )
                $startProcess = Start-Process `
                    -FilePath $pgCtl `
                    -ArgumentList $startArguments `
                    -WindowStyle Hidden `
                    -PassThru
                if (-not $startProcess.WaitForExit(60000)) {
                    Stop-Process -Id $startProcess.Id -Force -ErrorAction SilentlyContinue
                    throw "PostgreSQL pg_ctl did not finish within 60 seconds."
                }
                $startProcess.Refresh()
                if ($startProcess.ExitCode -ne 0) {
                    throw "PostgreSQL failed to start. Inspect the runtime log for details."
                }
                Assert-ExpectedServerOptions -DataDir $dataDir
            }

            $ignored = @(& $pgIsReady `
                -h $ListenAddress `
                -p $Port `
                -d postgres `
                -U $DatabaseUser 2>&1)
            if ($LASTEXITCODE -ne 0) {
                throw "PostgreSQL started but did not become ready on 127.0.0.1:15432."
            }

            Ensure-ControlDatabase `
                -Psql $psql `
                -CreateDb $createDb `
                -PasswordFile $passwordFile

            Set-MarkedDrive -Drive $mapping.Drive
            $keepMapping = $true
            Write-Output "running host=$ListenAddress port=$Port database=$ControlDatabase"
        }

        'stop' {
            if (Test-PostgresRunning -PgCtl $pgCtl -DataDir $dataDir) {
                Assert-ExpectedServerOptions -DataDir $dataDir
                $ignored = @(& $pgCtl -D $dataDir -t 60 -w stop -m fast 2>&1)
                if ($LASTEXITCODE -ne 0) {
                    throw "PostgreSQL failed to stop cleanly."
                }
            }

            Remove-RepoDriveMapping -Drive $mapping.Drive
            $mappingRemoved = $true
            Write-Output "stopped host=$ListenAddress port=$Port database=$ControlDatabase"
        }
    }
}
finally {
    if (($null -ne $mapping) -and (-not $keepMapping) -and (-not $mappingRemoved) -and $mapping.Created) {
        Remove-RepoDriveMapping -Drive $mapping.Drive
    }
}
