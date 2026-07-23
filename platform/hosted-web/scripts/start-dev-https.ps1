[CmdletBinding()]
param(
    [switch]$ValidateOnly,
    [ValidateRange(0, 60)]
    [int]$SmokeTestSeconds = 0
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$TrustedModuleRoot = Join-Path $PSHOME 'Modules'
$TrustedModuleRootItem = Get-Item -LiteralPath $TrustedModuleRoot -Force -ErrorAction Stop
if (-not $TrustedModuleRootItem.PSIsContainer -or ($TrustedModuleRootItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
    throw 'Windows PowerShell trusted module root must be a plain directory.'
}
foreach ($ModuleManifestRelativePath in @(
    'Microsoft.PowerShell.Security\Microsoft.PowerShell.Security.psd1',
    'Microsoft.PowerShell.Utility\Microsoft.PowerShell.Utility.psd1',
    'PKI\PKI.psd1',
    'CimCmdlets\CimCmdlets.psd1'
)) {
    $ModuleManifest = Join-Path $TrustedModuleRoot $ModuleManifestRelativePath
    $ModuleDirectoryItem = Get-Item -LiteralPath (Split-Path -Parent $ModuleManifest) -Force -ErrorAction Stop
    $ModuleManifestItem = Get-Item -LiteralPath $ModuleManifest -Force -ErrorAction Stop
    if (-not $ModuleDirectoryItem.PSIsContainer -or
        ($ModuleDirectoryItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -or
        $ModuleManifestItem.PSIsContainer -or
        ($ModuleManifestItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
        throw 'Windows PowerShell trusted module input must not contain a reparse point.'
    }
    Import-Module -Name $ModuleManifestItem.FullName -Force -ErrorAction Stop | Out-Null
}

$AppRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$WorkspaceRoot = (Resolve-Path (Join-Path $AppRoot '..\..')).Path
$RuntimeRoot = Join-Path $WorkspaceRoot '.runtime'
$TLSBaseRoot = Join-Path $RuntimeRoot 'dev-tls'
$CurrentUserSID = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$NormalizedUserSID = ($CurrentUserSID.ToLowerInvariant() -replace '[^a-z0-9-]', '-')
$TLSRoot = Join-Path $TLSBaseRoot ('user-' + $NormalizedUserSID)
$PFXPath = Join-Path $TLSRoot 'admin-web.pfx'
$PasswordPath = Join-Path $TLSRoot 'admin-web-pfx-password.txt'
$SmokeResultPath = Join-Path $RuntimeRoot 'hosted-web-https-smoke-result.json'
$HostedPort = 5175
$MaximumPFXBytes = 10MB
$MaximumPasswordBytes = 4KB

function Test-PathWithin {
    param([string]$Path, [string]$Root)
    $candidate = [IO.Path]::GetFullPath($Path).TrimEnd('\')
    $boundary = [IO.Path]::GetFullPath($Root).TrimEnd('\')
    return $candidate.StartsWith($boundary + '\', [StringComparison]::OrdinalIgnoreCase)
}

function Assert-NoReparseChain {
    param([string]$Path, [string]$Boundary)
    $current = [IO.Path]::GetFullPath($Path).TrimEnd('\')
    $stop = [IO.Path]::GetFullPath($Boundary).TrimEnd('\')
    if ($current -ne $stop -and -not (Test-PathWithin -Path $current -Root $stop)) {
        throw 'Hosted development TLS path escaped its workspace boundary.'
    }
    while ($true) {
        $item = Get-Item -LiteralPath $current -Force -ErrorAction Stop
        if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) {
            throw 'Hosted development TLS path must not contain a reparse point.'
        }
        if ($current.Equals($stop, [StringComparison]::OrdinalIgnoreCase)) { break }
        $parent = [IO.Path]::GetDirectoryName($current)
        if ([string]::IsNullOrWhiteSpace($parent) -or $parent -eq $current) {
            throw 'Hosted development TLS path has no trusted parent.'
        }
        $current = $parent.TrimEnd('\')
    }
}

function Resolve-SID {
    param([System.Security.Principal.IdentityReference]$Identity)
    return $Identity.Translate([System.Security.Principal.SecurityIdentifier]).Value
}

function Assert-PrivateFileACL {
    param([string]$Path)
    $acl = Get-Acl -LiteralPath $Path -ErrorAction Stop
    $currentSID = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $ownerReference = if ($acl.Owner.StartsWith('S-1-', [StringComparison]::OrdinalIgnoreCase)) {
        New-Object Security.Principal.SecurityIdentifier($acl.Owner)
    } else {
        New-Object Security.Principal.NTAccount($acl.Owner)
    }
    if ((Resolve-SID -Identity $ownerReference) -ne $currentSID) {
        throw 'Hosted development TLS input must be owned by the current Windows user.'
    }
    if (-not $acl.AreAccessRulesProtected) {
        throw 'Hosted development TLS input must not inherit access rules.'
    }
    $seenCurrent = $false
    $seenSystem = $false
    foreach ($entry in $acl.Access) {
        $sid = Resolve-SID -Identity $entry.IdentityReference
        if ($entry.AccessControlType -ne [Security.AccessControl.AccessControlType]::Allow -or
            ($sid -ne $currentSID -and $sid -ne 'S-1-5-18')) {
            throw 'Hosted development TLS input grants access outside the current user and SYSTEM.'
        }
        if ($sid -eq $currentSID) { $seenCurrent = $true }
        if ($sid -eq 'S-1-5-18') { $seenSystem = $true }
    }
    if (-not $seenCurrent -or -not $seenSystem) {
        throw 'Hosted development TLS input ACL is incomplete.'
    }
}

function Assert-PrivateDirectoryACL {
    param([string]$Path)
    $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    if (-not $item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
        throw 'Hosted development TLS user directory must be a plain directory.'
    }
    $acl = Get-Acl -LiteralPath $Path -ErrorAction Stop
    $currentSID = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $ownerReference = if ($acl.Owner.StartsWith('S-1-', [StringComparison]::OrdinalIgnoreCase)) {
        New-Object Security.Principal.SecurityIdentifier($acl.Owner)
    } else {
        New-Object Security.Principal.NTAccount($acl.Owner)
    }
    if ((Resolve-SID -Identity $ownerReference) -ne $currentSID -or -not $acl.AreAccessRulesProtected) {
        throw 'Hosted development TLS user directory owner or inheritance is invalid.'
    }
    $seenCurrent = $false
    $seenSystem = $false
    foreach ($entry in $acl.Access) {
        $sid = Resolve-SID -Identity $entry.IdentityReference
        if ($entry.AccessControlType -ne [Security.AccessControl.AccessControlType]::Allow -or
            ($sid -ne $currentSID -and $sid -ne 'S-1-5-18')) {
            throw 'Hosted development TLS user directory grants unexpected access.'
        }
        if ($sid -eq $currentSID) { $seenCurrent = $true }
        if ($sid -eq 'S-1-5-18') { $seenSystem = $true }
    }
    if (-not $seenCurrent -or -not $seenSystem) {
        throw 'Hosted development TLS user directory ACL is incomplete.'
    }
}

function Resolve-ControlledFile {
    param([string]$Path, [long]$MaximumBytes)
    $lexicalPath = [IO.Path]::GetFullPath($Path)
    if (-not (Test-PathWithin -Path $lexicalPath -Root $RuntimeRoot)) {
        throw 'Hosted development TLS file must stay inside the workspace runtime directory.'
    }
    Assert-NoReparseChain -Path ([IO.Path]::GetDirectoryName($lexicalPath)) -Boundary $WorkspaceRoot
    $item = Get-Item -LiteralPath $lexicalPath -Force -ErrorAction Stop
    if ($item.PSIsContainer -or $item -isnot [IO.FileInfo] -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
        throw 'Hosted development TLS input must be a plain regular file.'
    }
    if ($item.Length -le 0 -or $item.Length -gt $MaximumBytes) {
        throw 'Hosted development TLS input size is outside the allowed range.'
    }
    $resolvedPath = (Resolve-Path -LiteralPath $lexicalPath -ErrorAction Stop).Path
    if (-not $resolvedPath.Equals($lexicalPath, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Hosted development TLS input resolved to a different path.'
    }
    Assert-PrivateFileACL -Path $resolvedPath
    return [pscustomobject]@{ Path = $resolvedPath; Length = $item.Length; LastWriteTimeUtc = $item.LastWriteTimeUtc }
}

function Assert-UnchangedFile {
    param([object]$Snapshot, [long]$MaximumBytes)
    $item = Get-Item -LiteralPath $Snapshot.Path -Force -ErrorAction Stop
    if ($item.PSIsContainer -or $item -isnot [IO.FileInfo] -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -or
        $item.Length -le 0 -or $item.Length -gt $MaximumBytes -or $item.Length -ne $Snapshot.Length -or
        $item.LastWriteTimeUtc.Ticks -ne $Snapshot.LastWriteTimeUtc.Ticks) {
        throw 'Hosted development TLS input changed during validation.'
    }
}

# TLS-CERTIFICATE-VALIDATION-BEGIN
function Read-StrictDERLength {
    param([byte[]]$Data, [ref]$Offset, [int]$End)
    if ($Offset.Value -ge $End) { throw 'Development TLS SAN DER length is truncated.' }
    $first = [int]$Data[$Offset.Value]
    $Offset.Value++
    if (($first -band 0x80) -eq 0) { return $first }
    $count = $first -band 0x7f
    if ($count -eq 0 -or $count -gt 4 -or $Offset.Value + $count -gt $End) {
        throw 'Development TLS SAN DER length is invalid.'
    }
    if ($Data[$Offset.Value] -eq 0) { throw 'Development TLS SAN DER length is not minimal.' }
    [long]$length = 0
    for ($index = 0; $index -lt $count; $index++) {
        $length = ($length -shl 8) -bor [int]$Data[$Offset.Value]
        $Offset.Value++
    }
    if ($length -lt 128 -or $length -gt [int]::MaxValue -or $length -gt ($End - $Offset.Value)) {
        throw 'Development TLS SAN DER length is invalid.'
    }
    return [int]$length
}

function Assert-StrictLocalhostSAN {
    param([byte[]]$RawData)
    if ($null -eq $RawData -or $RawData.Length -lt 2) { throw 'Development TLS SAN DER is empty.' }
    [int]$offset = 0
    if ($RawData[$offset] -ne 0x30) { throw 'Development TLS SAN DER must start with a sequence.' }
    $offset++
    $sequenceLength = Read-StrictDERLength -Data $RawData -Offset ([ref]$offset) -End $RawData.Length
    if ($offset + $sequenceLength -ne $RawData.Length) { throw 'Development TLS SAN DER sequence length is invalid.' }
    $sequenceEnd = $RawData.Length
    $dnsFound = $false
    $ipFound = $false
    $localhostBytes = [Text.Encoding]::ASCII.GetBytes('localhost')
    while ($offset -lt $sequenceEnd) {
        $tag = [int]$RawData[$offset]
        $offset++
        $valueLength = Read-StrictDERLength -Data $RawData -Offset ([ref]$offset) -End $sequenceEnd
        if ($valueLength -gt ($sequenceEnd - $offset)) { throw 'Development TLS SAN GeneralName is truncated.' }
        $valueEnd = $offset + $valueLength
        if ($tag -eq 0x82) {
            if ($dnsFound -or $valueLength -ne $localhostBytes.Length) { throw 'Development TLS SAN dNSName is not exactly localhost.' }
            for ($index = 0; $index -lt $localhostBytes.Length; $index++) {
                if ($RawData[$offset + $index] -ne $localhostBytes[$index]) { throw 'Development TLS SAN dNSName is not exactly localhost.' }
            }
            $dnsFound = $true
        }
        elseif ($tag -eq 0x87) {
            if ($ipFound -or $valueLength -ne 4 -or $RawData[$offset] -ne 127 -or $RawData[$offset + 1] -ne 0 -or
                $RawData[$offset + 2] -ne 0 -or $RawData[$offset + 3] -ne 1) {
                throw 'Development TLS SAN iPAddress is not exactly 127.0.0.1.'
            }
            $ipFound = $true
        }
        else {
            throw 'Development TLS SAN contains an unsupported GeneralName.'
        }
        $offset = $valueEnd
    }
    if (-not $dnsFound -or -not $ipFound) { throw 'Development TLS SAN must contain localhost and 127.0.0.1.' }
}

function Assert-DevelopmentTLSCertificate {
    param([Security.Cryptography.X509Certificates.X509Certificate2]$Certificate)
    if ($null -eq $Certificate -or -not $Certificate.HasPrivateKey) {
        throw 'Development TLS certificate must contain a private key.'
    }
    $ekuExtensions = @($Certificate.Extensions | Where-Object { $_.Oid.Value -eq '2.5.29.37' })
    if ($ekuExtensions.Count -ne 1) { throw 'Development TLS certificate must contain exactly one EKU extension.' }
    $eku = [Security.Cryptography.X509Certificates.X509EnhancedKeyUsageExtension]$ekuExtensions[0]
    if (@($eku.EnhancedKeyUsages | Where-Object { $_.Value -eq '1.3.6.1.5.5.7.3.1' }).Count -ne 1) {
        throw 'Development TLS certificate EKU must contain serverAuth.'
    }
    $sanExtensions = @($Certificate.Extensions | Where-Object { $_.Oid.Value -eq '2.5.29.17' })
    if ($sanExtensions.Count -ne 1) { throw 'Development TLS certificate must contain exactly one SAN extension.' }
    Assert-StrictLocalhostSAN -RawData $sanExtensions[0].RawData
    $simpleName = $Certificate.GetNameInfo([Security.Cryptography.X509Certificates.X509NameType]::SimpleName, $false)
    if ($Certificate.Subject -ne 'CN=localhost' -or $simpleName -ne 'localhost' -or
        $Certificate.NotBefore.ToUniversalTime() -gt [DateTime]::UtcNow -or
        $Certificate.NotAfter.ToUniversalTime() -le [DateTime]::UtcNow) {
        throw 'Development TLS certificate is not a valid current localhost certificate.'
    }
}
# TLS-CERTIFICATE-VALIDATION-END

function Test-PortFree {
    param([int]$Port)
    $listener = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, $Port)
    try {
        $listener.Start()
        return $true
    } catch {
        return $false
    } finally {
        try { $listener.Stop() } catch {}
    }
}

function Wait-PortState {
    param([int]$Port, [bool]$Free, [int]$TimeoutSeconds)
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        if ((Test-PortFree -Port $Port) -eq $Free) { return $true }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $deadline)
    return $false
}

function Get-ControlledProcessTree {
    param([int]$RootProcessID)
    $all = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue)
    $ids = New-Object 'System.Collections.Generic.List[int]'
    $queue = New-Object 'System.Collections.Generic.Queue[int]'
    $queue.Enqueue($RootProcessID)
    while ($queue.Count -gt 0) {
        $parent = $queue.Dequeue()
        foreach ($child in $all | Where-Object { [int]$_.ParentProcessId -eq $parent }) {
            $childID = [int]$child.ProcessId
            if (-not $ids.Contains($childID)) { $ids.Add($childID); $queue.Enqueue($childID) }
        }
    }
    [array]::Reverse($ids)
    return @($ids) + @($RootProcessID)
}

function Stop-ControlledProcessTree {
    param([Diagnostics.Process]$RootProcess)
    if ($null -eq $RootProcess) { return }
    foreach ($processID in Get-ControlledProcessTree -RootProcessID $RootProcess.Id) {
        Stop-Process -Id $processID -Force -ErrorAction SilentlyContinue
    }
}

Assert-NoReparseChain -Path $TLSRoot -Boundary $WorkspaceRoot
Assert-PrivateDirectoryACL -Path $TLSRoot
$PFX = Resolve-ControlledFile -Path $PFXPath -MaximumBytes $MaximumPFXBytes
$Password = Resolve-ControlledFile -Path $PasswordPath -MaximumBytes $MaximumPasswordBytes
$PasswordBytes = $null
$Passphrase = $null
$SecurePassword = $null
try {
    $PasswordBytes = [IO.File]::ReadAllBytes($Password.Path)
    if ($PasswordBytes.Length -le 0 -or $PasswordBytes.Length -gt $MaximumPasswordBytes) {
        throw 'Hosted development TLS passphrase size is outside the allowed range.'
    }
    $Passphrase = [Text.Encoding]::UTF8.GetString($PasswordBytes).Trim()
    if ([string]::IsNullOrWhiteSpace($Passphrase) -or $Passphrase.Length -gt 1024) {
        throw 'Hosted development TLS passphrase file is invalid.'
    }
    Assert-UnchangedFile -Snapshot $PFX -MaximumBytes $MaximumPFXBytes
    Assert-UnchangedFile -Snapshot $Password -MaximumBytes $MaximumPasswordBytes
    $SecurePassword = ConvertTo-SecureString -String $Passphrase -AsPlainText -Force
    $PFXData = Get-PfxData -FilePath $PFX.Path -Password $SecurePassword -ErrorAction Stop
    if ($null -eq $PFXData -or $null -eq $PFXData.EndEntityCertificates -or $PFXData.EndEntityCertificates.Count -lt 1) {
        throw 'Hosted development TLS PFX contains no end-entity certificate.'
    }
    Assert-DevelopmentTLSCertificate -Certificate @($PFXData.EndEntityCertificates)[0]
    Assert-UnchangedFile -Snapshot $PFX -MaximumBytes $MaximumPFXBytes
    Assert-UnchangedFile -Snapshot $Password -MaximumBytes $MaximumPasswordBytes
    if ($ValidateOnly) {
        Write-Output 'hosted https inputs valid'
        exit 0
    }

    if (-not (Test-PortFree -Port $HostedPort)) {
        throw 'Hosted development port 5175 is already in use.'
    }
    $npm = Get-Command npm.cmd -ErrorAction Stop
    $VariableNames = @('HOSTED_DEV_TLS_PFX', 'HOSTED_DEV_TLS_PFX_PASSWORD', 'HOSTED_BACKEND_TARGET')
    $PreviousValues = @{}
    foreach ($Name in $VariableNames) { $PreviousValues[$Name] = [Environment]::GetEnvironmentVariable($Name, 'Process') }
    $PreviousLocation = Get-Location
    $Process = $null
    $ExitCode = 1
    try {
        $env:HOSTED_DEV_TLS_PFX = $PFX.Path
        $env:HOSTED_DEV_TLS_PFX_PASSWORD = $Passphrase
        $env:HOSTED_BACKEND_TARGET = 'http://127.0.0.1:8080'
        $BuildProcess = Start-Process -FilePath $npm.Source -ArgumentList @('run', 'build') -WorkingDirectory $AppRoot -WindowStyle Hidden -Wait -PassThru
        if ($BuildProcess.ExitCode -ne 0) {
            throw 'Hosted production build failed; HTTPS preview was not started.'
        }
        $Process = Start-Process -FilePath $npm.Source -ArgumentList @('exec', '--', 'vite', 'preview', '--host', '127.0.0.1', '--port', '5175', '--strictPort', '--configLoader', 'runner') -WorkingDirectory $AppRoot -WindowStyle Hidden -PassThru
        if (-not (Wait-PortState -Port $HostedPort -Free $false -TimeoutSeconds 20)) {
            throw 'Hosted HTTPS process did not bind port 5175.'
        }
        Write-Output 'hosted https ready host=127.0.0.1 port=5175'
        if ($SmokeTestSeconds -gt 0) {
            Start-Sleep -Seconds $SmokeTestSeconds
            $ExitCode = 0
        } else {
            $Process.WaitForExit()
            $ExitCode = $Process.ExitCode
        }
    }
    finally {
        foreach ($Name in $VariableNames) { [Environment]::SetEnvironmentVariable($Name, $PreviousValues[$Name], 'Process') }
        if ($null -ne $PasswordBytes) { [Array]::Clear($PasswordBytes, 0, $PasswordBytes.Length) }
        $Passphrase = $null
        try { Set-Location $PreviousLocation } catch { $ExitCode = 1 }
        Stop-ControlledProcessTree -RootProcess $Process
        $PortReleased = Wait-PortState -Port $HostedPort -Free $true -TimeoutSeconds 10
        if (-not $PortReleased) { $ExitCode = 1 }
        if ($SmokeTestSeconds -gt 0) {
            $restored = $true
            foreach ($Name in $VariableNames) {
                if ([Environment]::GetEnvironmentVariable($Name, 'Process') -ne $PreviousValues[$Name]) { $restored = $false }
            }
            [pscustomobject]@{ environment_restored = $restored; port_released = $PortReleased; location_restored = ((Get-Location).Path -eq $PreviousLocation.Path) } |
                ConvertTo-Json -Compress | Set-Content -LiteralPath $SmokeResultPath -Encoding UTF8
        }
    }
    exit $ExitCode
}
finally {
    if ($null -ne $PasswordBytes) { [Array]::Clear($PasswordBytes, 0, $PasswordBytes.Length) }
    if ($null -ne $SecurePassword -and $SecurePassword -is [IDisposable]) { $SecurePassword.Dispose() }
    $Passphrase = $null
}
