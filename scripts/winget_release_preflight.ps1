[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Tag,
    [Parameter(Mandatory = $true)][string]$ReleaseJsonPath,
    [string]$OutputPath
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$identifier = 'RicardoCabral.ajq'
$packageApi = 'repos/microsoft/winget-pkgs/contents/manifests/r/RicardoCabral/ajq'

if ($Tag -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+$') {
    throw "WinGet preflight requires a vX.Y.Z tag, got $Tag"
}
if ([string]::IsNullOrWhiteSpace($env:WINGET_TOKEN)) {
    throw 'missing required secret: WINGET_TOKEN'
}

# winget-releaser updates an existing package; it cannot bootstrap the first
# immutable manifest. Query the public upstream before invoking it so a missing
# package has an actionable maintainer diagnostic rather than an opaque action
# failure. GH_BIN exists solely to make this public API boundary testable.
$gh = if ($env:GH_BIN) { $env:GH_BIN } else { (Get-Command gh -CommandType Application -ErrorAction Stop | Select-Object -First 1).Source }
$apiOutput = (& $gh api $packageApi 2>&1 | Out-String)
$apiExit = $LASTEXITCODE
if ($apiExit -ne 0) {
    if ($apiOutput -match '(?i)(HTTP 404|not found)') {
        throw "WinGet bootstrap required: $identifier is not yet available in microsoft/winget-pkgs. The initial immutable MSI manifest is a release-maintainer-owned manual submission and must merge and propagate before automated updates can run."
    }
    throw "WinGet preflight could not verify existing $identifier package in microsoft/winget-pkgs; retry after GitHub API availability is restored."
}

$version = $Tag.Substring(1)
$expectedAsset = "ajq_${version}_Windows_x86_64.msi"
$release = Get-Content -Raw -LiteralPath $ReleaseJsonPath | ConvertFrom-Json
if ($release.tagName -ne $Tag) {
    throw "WinGet preflight release tag mismatch for ${identifier}: expected $Tag"
}
$matches = @($release.assets | Where-Object { $_.name -ceq $expectedAsset })
if ($matches.Count -ne 1) {
    throw "WinGet preflight for $identifier tag $Tag requires exactly one asset $expectedAsset"
}

# This action infers WiX from the selected .msi. Keep the generated manifest
# contract explicit and fail before submission if this invariant is changed.
$installerType = 'wix'
$architecture = 'x64'
$regex = '^' + [regex]::Escape($expectedAsset) + '$'
if ($OutputPath) {
    @(
        "tag=$Tag"
        "version=$version"
        "asset=$expectedAsset"
        "regex=$regex"
        "identifier=$identifier"
        "installer_type=$installerType"
        "architecture=$architecture"
    ) | Add-Content -LiteralPath $OutputPath
} else {
    [PSCustomObject]@{ Tag = $Tag; Version = $version; Asset = $expectedAsset; Regex = $regex; Identifier = $identifier; InstallerType = $installerType; Architecture = $architecture }
}
