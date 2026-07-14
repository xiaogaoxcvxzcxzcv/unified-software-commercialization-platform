[CmdletBinding()]
param(
    [int]$Port = 5174
)

$ErrorActionPreference = 'Stop'

function Assert-WithinRoot {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Target
    )

    $rootPath = [IO.Path]::GetFullPath($Root).TrimEnd('\')
    $targetPath = [IO.Path]::GetFullPath($Target)
    if (-not $targetPath.StartsWith($rootPath + '\', [StringComparison]::OrdinalIgnoreCase)) {
        throw "Development TLS output must stay inside .runtime."
    }
}

$appRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$workspaceRoot = (Resolve-Path (Join-Path $appRoot '..\..')).Path
$runtimeRoot = Join-Path $workspaceRoot '.runtime'
$tlsRoot = Join-Path $runtimeRoot 'dev-tls'
$pfxPath = Join-Path $tlsRoot 'admin-web.pfx'
$passwordPath = Join-Path $tlsRoot 'admin-web-pfx-password.txt'

Assert-WithinRoot -Root $runtimeRoot -Target $pfxPath
Assert-WithinRoot -Root $runtimeRoot -Target $passwordPath
New-Item -ItemType Directory -Force -Path $tlsRoot | Out-Null

if (-not (Test-Path -LiteralPath $pfxPath) -or -not (Test-Path -LiteralPath $passwordPath)) {
    $passwordBytes = New-Object byte[] 32
    $random = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $random.GetBytes($passwordBytes)
    }
    finally {
        $random.Dispose()
    }
    $password = [Convert]::ToBase64String($passwordBytes)
    [IO.File]::WriteAllText($passwordPath, $password, (New-Object Text.UTF8Encoding($false)))
    $securePassword = ConvertTo-SecureString -String $password -AsPlainText -Force
    $certificate = $null
    try {
        $certificate = New-SelfSignedCertificate `
            -Subject 'CN=localhost' `
            -CertStoreLocation 'Cert:\CurrentUser\My' `
            -KeyAlgorithm RSA `
            -KeyLength 2048 `
            -HashAlgorithm SHA256 `
            -KeyExportPolicy Exportable `
            -NotAfter ([DateTime]::UtcNow.AddYears(1)) `
            -TextExtension @('2.5.29.17={text}DNS=localhost&IPAddress=127.0.0.1')
        Export-PfxCertificate -Cert $certificate -FilePath $pfxPath -Password $securePassword | Out-Null
    }
    catch {
        Remove-Item -LiteralPath $pfxPath, $passwordPath -Force -ErrorAction SilentlyContinue
        throw
    }
    finally {
        if ($null -ne $certificate) {
            Remove-Item -LiteralPath ("Cert:\CurrentUser\My\" + $certificate.Thumbprint) -Force -ErrorAction SilentlyContinue
        }
    }
}

$env:PLATFORM_ADMIN_DEV_TLS_PFX = $pfxPath
$env:PLATFORM_ADMIN_DEV_TLS_PFX_PASSWORD = ([IO.File]::ReadAllText($passwordPath)).Trim()

Set-Location $appRoot
& npm.cmd exec -- vite --host 127.0.0.1 --port $Port --strictPort --configLoader runner
exit $LASTEXITCODE
