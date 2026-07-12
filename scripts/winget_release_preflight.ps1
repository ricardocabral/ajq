[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Tag,
    [Parameter(Mandatory = $true)][string]$ReleaseJsonPath,
    [string]$OutputPath
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$identifier = 'Ricardocabral.ajq'

if ($Tag -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+$') {
    throw "WinGet preflight requires a vX.Y.Z tag, got $Tag"
}
if ([string]::IsNullOrWhiteSpace($env:WINGET_TOKEN)) {
    throw 'missing required secret: WINGET_TOKEN'
}

$version = $Tag.Substring(1)
$expectedAsset = "ajq_${version}_Windows_x86_64.zip"
$release = Get-Content -Raw -LiteralPath $ReleaseJsonPath | ConvertFrom-Json
if ($release.tagName -ne $Tag) {
    throw "WinGet preflight release tag mismatch for $identifier: expected $Tag"
}
$matches = @($release.assets | Where-Object { $_.name -ceq $expectedAsset })
if ($matches.Count -ne 1) {
    throw "WinGet preflight for $identifier tag $Tag requires exactly one asset $expectedAsset"
}

$regex = '^' + [regex]::Escape($expectedAsset) + '$'
if ($OutputPath) {
    @(
        "tag=$Tag"
        "version=$version"
        "asset=$expectedAsset"
        "regex=$regex"
    ) | Add-Content -LiteralPath $OutputPath
} else {
    [PSCustomObject]@{ Tag = $Tag; Version = $version; Asset = $expectedAsset; Regex = $regex }
}
