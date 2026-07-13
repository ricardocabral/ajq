[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Tag,
    [string]$OutputPath
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if ($Tag -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+$') {
    throw "Windows MSI requires a stable vX.Y.Z tag, got $Tag"
}

function Get-UuidV5([string]$Namespace, [string]$Name) {
    $namespaceBytes = [guid]$Namespace
    $bytes = $namespaceBytes.ToByteArray()
    [array]::Reverse($bytes, 0, 4)
    [array]::Reverse($bytes, 4, 2)
    [array]::Reverse($bytes, 6, 2)
    $nameBytes = [Text.Encoding]::UTF8.GetBytes($Name)
    $input = [byte[]]::new($bytes.Length + $nameBytes.Length)
    [Array]::Copy($bytes, 0, $input, 0, $bytes.Length)
    [Array]::Copy($nameBytes, 0, $input, $bytes.Length, $nameBytes.Length)
    $hash = [Security.Cryptography.SHA1]::HashData($input)
    $hash[6] = ($hash[6] -band 0x0f) -bor 0x50
    $hash[8] = ($hash[8] -band 0x3f) -bor 0x80
    $hex = [BitConverter]::ToString($hash[0..15]).Replace('-', '')
    return '{{{0}-{1}-{2}-{3}-{4}}}' -f $hex.Substring(0, 8), $hex.Substring(8, 4), $hex.Substring(12, 4), $hex.Substring(16, 4), $hex.Substring(20, 12)
}

$version = $Tag.Substring(1)
$productCode = Get-UuidV5 'BA73FC93-6FEE-410C-A647-596319F7BC1F' "RicardoCabral.ajq/x64/$version"
$packageCode = Get-UuidV5 'BA73FC93-6FEE-410C-A647-596319F7BC1F' "RicardoCabral.ajq/x64/$version/package"
$zipAsset = "ajq_${version}_Windows_x86_64.zip"
$msiAsset = "ajq_${version}_Windows_x86_64.msi"

$values = @(
    "version=$version"
    "product_code=$productCode"
    "package_code=$packageCode"
    "zip_asset=$zipAsset"
    "msi_asset=$msiAsset"
)
if ($OutputPath) {
    $values | Add-Content -LiteralPath $OutputPath
} else {
    $values
}
