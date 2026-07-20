[CmdletBinding()]
param(
    [ValidateRange(1024, 65535)]
    [int]$Port = 5174,
    [switch]$ProvisionTLSOnly,
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
        throw 'Admin development TLS path escaped its workspace boundary.'
    }
    while ($true) {
        $item = Get-Item -LiteralPath $current -Force -ErrorAction Stop
        if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) {
            throw 'Admin development TLS path must not contain a reparse point.'
        }
        if ($current.Equals($stop, [StringComparison]::OrdinalIgnoreCase)) { break }
        $parent = [IO.Path]::GetDirectoryName($current)
        if ([string]::IsNullOrWhiteSpace($parent) -or $parent -eq $current) {
            throw 'Admin development TLS path has no trusted parent.'
        }
        $current = $parent.TrimEnd('\')
    }
}

function Ensure-ControlledDirectory {
    param([string]$Path, [string]$Parent, [string]$Boundary)
    $lexicalPath = [IO.Path]::GetFullPath($Path)
    if (-not (Test-PathWithin -Path $lexicalPath -Root $Boundary)) {
        throw 'Admin development TLS directory must stay inside the workspace.'
    }
    Assert-NoReparseChain -Path $Parent -Boundary $Boundary
    if (-not (Test-Path -LiteralPath $lexicalPath)) {
        New-Item -ItemType Directory -Path $lexicalPath -ErrorAction Stop | Out-Null
    }
    $item = Get-Item -LiteralPath $lexicalPath -Force -ErrorAction Stop
    if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) {
        throw 'Admin development TLS path must not contain a reparse point.'
    }
    if (-not $item.PSIsContainer) {
        throw 'Admin development TLS directory must be a plain directory.'
    }
    Assert-NoReparseChain -Path $lexicalPath -Boundary $Boundary
}

function Resolve-SID {
    param([System.Security.Principal.IdentityReference]$Identity)
    return $Identity.Translate([System.Security.Principal.SecurityIdentifier]).Value
}

function Test-FileOwnedByCurrentUser {
    param([string]$Path)
    $acl = Get-Acl -LiteralPath $Path -ErrorAction Stop
    $ownerReference = if ($acl.Owner.StartsWith('S-1-', [StringComparison]::OrdinalIgnoreCase)) {
        New-Object Security.Principal.SecurityIdentifier($acl.Owner)
    } else {
        New-Object Security.Principal.NTAccount($acl.Owner)
    }
    return (Resolve-SID -Identity $ownerReference) -eq [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
}

function Assert-PrivateFileACL {
    param([string]$Path)
    $acl = Get-Acl -LiteralPath $Path -ErrorAction Stop
    $currentSID = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $systemSID = 'S-1-5-18'
    $ownerReference = if ($acl.Owner.StartsWith('S-1-', [StringComparison]::OrdinalIgnoreCase)) {
        New-Object Security.Principal.SecurityIdentifier($acl.Owner)
    } else {
        New-Object Security.Principal.NTAccount($acl.Owner)
    }
    if ((Resolve-SID -Identity $ownerReference) -ne $currentSID) {
        throw 'Admin development TLS file must be owned by the current Windows user.'
    }
    if (-not $acl.AreAccessRulesProtected) {
        throw 'Admin development TLS file must not inherit access rules.'
    }
    $seenCurrent = $false
    $seenSystem = $false
    foreach ($entry in $acl.Access) {
        $sid = Resolve-SID -Identity $entry.IdentityReference
        if ($entry.AccessControlType -ne [Security.AccessControl.AccessControlType]::Allow -or
            ($sid -ne $currentSID -and $sid -ne $systemSID)) {
            throw 'Admin development TLS file grants access outside the current user and SYSTEM.'
        }
        if ($sid -eq $currentSID) { $seenCurrent = $true }
        if ($sid -eq $systemSID) { $seenSystem = $true }
    }
    if (-not $seenCurrent -or -not $seenSystem) {
        throw 'Admin development TLS file ACL is incomplete.'
    }
}

function Set-PrivateFileACL {
    param([string]$Path)
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $systemSID = New-Object Security.Principal.SecurityIdentifier('S-1-5-18')
    if (-not (Test-FileOwnedByCurrentUser -Path $Path)) {
        throw 'Admin development TLS file is not owned by the current Windows user.'
    }
    $acl = New-Object Security.AccessControl.FileSecurity
    $acl.SetAccessRuleProtection($true, $false)
    $currentRule = New-Object Security.AccessControl.FileSystemAccessRule(
        $identity.User,
        [Security.AccessControl.FileSystemRights]::FullControl,
        [Security.AccessControl.AccessControlType]::Allow
    )
    $systemRule = New-Object Security.AccessControl.FileSystemAccessRule(
        $systemSID,
        [Security.AccessControl.FileSystemRights]::FullControl,
        [Security.AccessControl.AccessControlType]::Allow
    )
    [void]$acl.AddAccessRule($currentRule)
    [void]$acl.AddAccessRule($systemRule)
    $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    if ($item.PSIsContainer -or $item -isnot [IO.FileInfo] -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
        throw 'Admin development TLS ACL target must remain a plain regular file.'
    }
    $item.SetAccessControl($acl)
    Assert-PrivateFileACL -Path $Path
}

function Assert-PrivateDirectoryACL {
    param([string]$Path)
    $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    if (-not $item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
        throw 'Admin development TLS user directory must be a plain directory.'
    }
    $acl = Get-Acl -LiteralPath $Path -ErrorAction Stop
    $currentSID = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $ownerReference = if ($acl.Owner.StartsWith('S-1-', [StringComparison]::OrdinalIgnoreCase)) {
        New-Object Security.Principal.SecurityIdentifier($acl.Owner)
    } else {
        New-Object Security.Principal.NTAccount($acl.Owner)
    }
    if ((Resolve-SID -Identity $ownerReference) -ne $currentSID -or -not $acl.AreAccessRulesProtected) {
        throw 'Admin development TLS user directory owner or inheritance is invalid.'
    }
    $seenCurrent = $false
    $seenSystem = $false
    foreach ($entry in $acl.Access) {
        $sid = Resolve-SID -Identity $entry.IdentityReference
        if ($entry.AccessControlType -ne [Security.AccessControl.AccessControlType]::Allow -or
            ($sid -ne $currentSID -and $sid -ne 'S-1-5-18')) {
            throw 'Admin development TLS user directory grants unexpected access.'
        }
        if ($sid -eq $currentSID) { $seenCurrent = $true }
        if ($sid -eq 'S-1-5-18') { $seenSystem = $true }
    }
    if (-not $seenCurrent -or -not $seenSystem) {
        throw 'Admin development TLS user directory ACL is incomplete.'
    }
}

function Set-PrivateDirectoryACL {
    param([string]$Path)
    if (-not (Test-FileOwnedByCurrentUser -Path $Path)) {
        throw 'Admin development TLS user directory is not owned by the current Windows user.'
    }
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $systemSID = New-Object Security.Principal.SecurityIdentifier('S-1-5-18')
    $acl = New-Object Security.AccessControl.DirectorySecurity
    $acl.SetAccessRuleProtection($true, $false)
    $inheritance = [Security.AccessControl.InheritanceFlags]::ContainerInherit -bor [Security.AccessControl.InheritanceFlags]::ObjectInherit
    $propagation = [Security.AccessControl.PropagationFlags]::None
    [void]$acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($identity.User, [Security.AccessControl.FileSystemRights]::FullControl, $inheritance, $propagation, [Security.AccessControl.AccessControlType]::Allow)))
    [void]$acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($systemSID, [Security.AccessControl.FileSystemRights]::FullControl, $inheritance, $propagation, [Security.AccessControl.AccessControlType]::Allow)))
    $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    $item.SetAccessControl($acl)
    Assert-PrivateDirectoryACL -Path $Path
}

function Resolve-ControlledFile {
    param([string]$Path, [long]$MaximumBytes)
    $lexicalPath = [IO.Path]::GetFullPath($Path)
    if (-not (Test-PathWithin -Path $lexicalPath -Root $RuntimeRoot)) {
        throw 'Admin development TLS file must stay inside the workspace runtime directory.'
    }
    Assert-NoReparseChain -Path ([IO.Path]::GetDirectoryName($lexicalPath)) -Boundary $WorkspaceRoot
    $item = Get-Item -LiteralPath $lexicalPath -Force -ErrorAction Stop
    if ($item.PSIsContainer -or $item -isnot [IO.FileInfo] -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
        throw 'Admin development TLS material must be a plain regular file.'
    }
    if ($item.Length -le 0 -or $item.Length -gt $MaximumBytes) {
        throw 'Admin development TLS material size is outside the allowed range.'
    }
    $resolvedPath = (Resolve-Path -LiteralPath $lexicalPath -ErrorAction Stop).Path
    if (-not $resolvedPath.Equals($lexicalPath, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Admin development TLS material resolved to a different path.'
    }
    return [pscustomobject]@{
        Path = $resolvedPath
        Length = $item.Length
        LastWriteTimeUtc = $item.LastWriteTimeUtc
    }
}

function Assert-UnchangedFile {
    param([object]$Snapshot, [long]$MaximumBytes)
    $item = Get-Item -LiteralPath $Snapshot.Path -Force -ErrorAction Stop
    if ($item.PSIsContainer -or $item -isnot [IO.FileInfo] -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -or
        $item.Length -le 0 -or $item.Length -gt $MaximumBytes -or $item.Length -ne $Snapshot.Length -or
        $item.LastWriteTimeUtc.Ticks -ne $Snapshot.LastWriteTimeUtc.Ticks) {
        throw 'Admin development TLS material changed during validation.'
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

function Read-ValidatedPassphrase {
    param([object]$PasswordSnapshot)
    $bytes = [IO.File]::ReadAllBytes($PasswordSnapshot.Path)
    try {
        if ($bytes.Length -le 0 -or $bytes.Length -gt $MaximumPasswordBytes) {
            throw 'Admin development TLS passphrase size is outside the allowed range.'
        }
        $value = [Text.Encoding]::UTF8.GetString($bytes).Trim()
        if ([string]::IsNullOrWhiteSpace($value) -or $value.Length -gt 1024) {
            throw 'Admin development TLS passphrase file is invalid.'
        }
        Assert-UnchangedFile -Snapshot $PasswordSnapshot -MaximumBytes $MaximumPasswordBytes
        return $value
    }
    finally {
        [Array]::Clear($bytes, 0, $bytes.Length)
    }
}

function Assert-PFXPair {
    param([object]$PFXSnapshot, [object]$PasswordSnapshot)
    $passphrase = $null
    $securePassword = $null
    try {
        $passphrase = Read-ValidatedPassphrase -PasswordSnapshot $PasswordSnapshot
        $securePassword = ConvertTo-SecureString -String $passphrase -AsPlainText -Force
        $pfxData = Get-PfxData -FilePath $PFXSnapshot.Path -Password $securePassword -ErrorAction Stop
        if ($null -eq $pfxData -or $null -eq $pfxData.EndEntityCertificates -or $pfxData.EndEntityCertificates.Count -lt 1) {
            throw 'Admin development TLS PFX contains no end-entity certificate.'
        }
        Assert-DevelopmentTLSCertificate -Certificate @($pfxData.EndEntityCertificates)[0]
        Assert-UnchangedFile -Snapshot $PFXSnapshot -MaximumBytes $MaximumPFXBytes
        Assert-UnchangedFile -Snapshot $PasswordSnapshot -MaximumBytes $MaximumPasswordBytes
    }
    finally {
        if ($null -ne $securePassword -and $securePassword -is [IDisposable]) { $securePassword.Dispose() }
        $passphrase = $null
    }
}

function New-TLSMaterial {
    param(
        [Parameter(Mandatory = $true)][string]$PFXTarget,
        [Parameter(Mandatory = $true)][string]$PasswordTarget
    )
    if (-not (Test-PathWithin -Path $PFXTarget -Root $TLSRoot) -or
        -not (Test-PathWithin -Path $PasswordTarget -Root $TLSRoot)) {
        throw 'Admin development TLS generation target escaped its controlled directory.'
    }
    Assert-NoReparseChain -Path ([IO.Path]::GetDirectoryName($PFXTarget)) -Boundary $WorkspaceRoot
    if ((Test-Path -LiteralPath $PFXTarget) -or (Test-Path -LiteralPath $PasswordTarget)) {
        throw 'Admin development TLS generation target already exists.'
    }
    $randomBytes = New-Object byte[] 32
    $passwordBytes = $null
    $passphrase = $null
    $securePassword = $null
    $random = [Security.Cryptography.RandomNumberGenerator]::Create()
    $certificate = $null
    $createdPFX = $false
    $createdPassword = $false
    try {
        $random.GetBytes($randomBytes)
        $passphrase = [Convert]::ToBase64String($randomBytes)
        $passwordBytes = (New-Object Text.UTF8Encoding($false)).GetBytes($passphrase)
        [IO.File]::WriteAllBytes($PasswordTarget, $passwordBytes)
        $createdPassword = $true
        Set-PrivateFileACL -Path $PasswordTarget

        $securePassword = ConvertTo-SecureString -String $passphrase -AsPlainText -Force
        $certificate = New-SelfSignedCertificate `
            -Subject 'CN=localhost' `
            -CertStoreLocation 'Cert:\CurrentUser\My' `
            -KeyAlgorithm RSA `
            -KeyLength 2048 `
            -HashAlgorithm SHA256 `
            -KeyExportPolicy Exportable `
            -NotAfter ([DateTime]::UtcNow.AddYears(1)) `
            -TextExtension @(
                '2.5.29.17={text}DNS=localhost&IPAddress=127.0.0.1',
                '2.5.29.37={text}1.3.6.1.5.5.7.3.1'
            ) `
            -ErrorAction Stop
        Export-PfxCertificate -Cert $certificate -FilePath $PFXTarget -Password $securePassword -ErrorAction Stop | Out-Null
        $createdPFX = $true
        Set-PrivateFileACL -Path $PFXTarget
    }
    catch {
        if ($createdPFX) { Remove-Item -LiteralPath $PFXTarget -Force -ErrorAction SilentlyContinue }
        if ($createdPassword) { Remove-Item -LiteralPath $PasswordTarget -Force -ErrorAction SilentlyContinue }
        throw
    }
    finally {
        if ($null -ne $certificate) {
            Remove-Item -LiteralPath ('Cert:\CurrentUser\My\' + $certificate.Thumbprint) -Force -ErrorAction SilentlyContinue
        }
        if ($null -ne $securePassword -and $securePassword -is [IDisposable]) { $securePassword.Dispose() }
        if ($null -ne $passwordBytes) { [Array]::Clear($passwordBytes, 0, $passwordBytes.Length) }
        [Array]::Clear($randomBytes, 0, $randomBytes.Length)
        $passphrase = $null
        $random.Dispose()
    }
}

function Ensure-TLSMaterial {
    Ensure-ControlledDirectory -Path $RuntimeRoot -Parent $WorkspaceRoot -Boundary $WorkspaceRoot
    Ensure-ControlledDirectory -Path $TLSBaseRoot -Parent $RuntimeRoot -Boundary $WorkspaceRoot
    Ensure-ControlledDirectory -Path $TLSRoot -Parent $TLSBaseRoot -Boundary $WorkspaceRoot
    Set-PrivateDirectoryACL -Path $TLSRoot

    $pfxExists = Test-Path -LiteralPath $PFXPath -PathType Leaf
    $passwordExists = Test-Path -LiteralPath $PasswordPath -PathType Leaf
    if ($pfxExists -ne $passwordExists) {
        throw 'Admin development TLS material is incomplete; existing files will not be replaced.'
    }
    if (-not $pfxExists) {
        New-TLSMaterial -PFXTarget $PFXPath -PasswordTarget $PasswordPath
    }

    $pfx = Resolve-ControlledFile -Path $PFXPath -MaximumBytes $MaximumPFXBytes
    $password = Resolve-ControlledFile -Path $PasswordPath -MaximumBytes $MaximumPasswordBytes
    Assert-PFXPair -PFXSnapshot $pfx -PasswordSnapshot $password

    $pfxHash = (Get-FileHash -LiteralPath $pfx.Path -Algorithm SHA256 -ErrorAction Stop).Hash
    $passwordHash = (Get-FileHash -LiteralPath $password.Path -Algorithm SHA256 -ErrorAction Stop).Hash
    if (-not (Test-FileOwnedByCurrentUser -Path $pfx.Path) -or
        -not (Test-FileOwnedByCurrentUser -Path $password.Path)) {
        throw 'Admin development TLS material must be owned by the current Windows user.'
    }
    Set-PrivateFileACL -Path $pfx.Path
    Set-PrivateFileACL -Path $password.Path
    if ((Get-FileHash -LiteralPath $pfx.Path -Algorithm SHA256 -ErrorAction Stop).Hash -ne $pfxHash -or
        (Get-FileHash -LiteralPath $password.Path -Algorithm SHA256 -ErrorAction Stop).Hash -ne $passwordHash) {
        throw 'Admin development TLS ACL migration changed file content.'
    }
    Assert-PFXPair -PFXSnapshot (Resolve-ControlledFile -Path $PFXPath -MaximumBytes $MaximumPFXBytes) `
        -PasswordSnapshot (Resolve-ControlledFile -Path $PasswordPath -MaximumBytes $MaximumPasswordBytes)
}

function Test-PortFree {
    param([int]$TargetPort)
    $listener = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, $TargetPort)
    try {
        $listener.Start()
        return $true
    }
    catch {
        return $false
    }
    finally {
        try { $listener.Stop() } catch {}
    }
}

function Wait-PortState {
    param([int]$TargetPort, [bool]$Free, [int]$TimeoutSeconds)
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        if ((Test-PortFree -TargetPort $TargetPort) -eq $Free) { return $true }
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

Ensure-TLSMaterial
if ($ProvisionTLSOnly) {
    Write-Output 'admin https TLS provisioned'
    exit 0
}

if (-not (Test-PortFree -TargetPort $Port)) {
    throw "Admin development port $Port is already in use."
}

$PFX = Resolve-ControlledFile -Path $PFXPath -MaximumBytes $MaximumPFXBytes
$Password = Resolve-ControlledFile -Path $PasswordPath -MaximumBytes $MaximumPasswordBytes
$PasswordBytes = $null
$Passphrase = $null
$VariableNames = @('PLATFORM_ADMIN_DEV_TLS_PFX', 'PLATFORM_ADMIN_DEV_TLS_PFX_PASSWORD')
$PreviousValues = @{}
foreach ($Name in $VariableNames) { $PreviousValues[$Name] = [Environment]::GetEnvironmentVariable($Name, 'Process') }
$Process = $null
$ExitCode = 1
try {
    $PasswordBytes = [IO.File]::ReadAllBytes($Password.Path)
    $Passphrase = [Text.Encoding]::UTF8.GetString($PasswordBytes).Trim()
    if ([string]::IsNullOrWhiteSpace($Passphrase) -or $Passphrase.Length -gt 1024) {
        throw 'Admin development TLS passphrase file is invalid.'
    }
    Assert-UnchangedFile -Snapshot $PFX -MaximumBytes $MaximumPFXBytes
    Assert-UnchangedFile -Snapshot $Password -MaximumBytes $MaximumPasswordBytes
    $npm = Get-Command npm.cmd -ErrorAction Stop
    $env:PLATFORM_ADMIN_DEV_TLS_PFX = $PFX.Path
    $env:PLATFORM_ADMIN_DEV_TLS_PFX_PASSWORD = $Passphrase
    $Process = Start-Process -FilePath $npm.Source `
        -ArgumentList @('exec', '--', 'vite', '--host', '127.0.0.1', '--port', [string]$Port, '--strictPort', '--configLoader', 'runner') `
        -WorkingDirectory $AppRoot `
        -WindowStyle Hidden `
        -PassThru
    if (-not (Wait-PortState -TargetPort $Port -Free $false -TimeoutSeconds 20)) {
        throw "Admin HTTPS process did not bind port $Port."
    }
    Write-Output "admin https ready host=127.0.0.1 port=$Port"
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
    Stop-ControlledProcessTree -RootProcess $Process
    if (-not (Wait-PortState -TargetPort $Port -Free $true -TimeoutSeconds 10)) { $ExitCode = 1 }
}
exit $ExitCode
