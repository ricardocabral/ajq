[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][ValidateSet('winget')][string]$Channel,
    [Parameter(Mandatory = $true)][string]$Tag
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if ($Tag -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+$') { throw "tag must be vX.Y.Z, got $Tag" }
$version = $Tag.Substring(1)
$wingetPath = $env:WINGET_BIN
if (-not $wingetPath) {
    $winget = Get-Command winget -CommandType Application -ErrorAction SilentlyContinue
    if (-not $winget) { throw 'required tool not found: winget' }
    $wingetPath = $winget.Source
}
if (-not (Test-Path -LiteralPath $wingetPath -PathType Leaf)) { throw 'required tool not found: winget' }

# Ignore uninstall failure when the package is absent, then refresh the source.
& $wingetPath uninstall --id Ricardocabral.ajq --exact --silent 2>$null
& $wingetPath source update
& $wingetPath install --id Ricardocabral.ajq --exact --version $version --source winget --accept-package-agreements --accept-source-agreements --silent
$installed = (& $wingetPath list --id Ricardocabral.ajq --exact | Out-String)
if ($installed -notmatch "(?m)\b$([regex]::Escape($version))\b") { throw "installed WinGet package version mismatch: expected $version" }

# Portable WinGet aliases live here; never use an arbitrary older ajq on PATH.
$ajq = if ($env:AJQ_PACKAGE_EXECUTABLE) { $env:AJQ_PACKAGE_EXECUTABLE } else { Join-Path $env:LOCALAPPDATA 'Microsoft\WinGet\Links\ajq.exe' }
if (-not (Test-Path -LiteralPath $ajq -PathType Leaf)) { throw "installed WinGet executable not found: $ajq" }
$actualVersion = (& $ajq --version | Out-String).Trim()
if ($actualVersion -notmatch [regex]::Escape($version)) { throw "ajq version mismatch: expected $version, got $actualVersion" }

$temp = Join-Path ([System.IO.Path]::GetTempPath()) ("ajq-package-smoke-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $temp | Out-Null
try {
    $env:HOME = Join-Path $temp 'home'
    $env:XDG_CONFIG_HOME = Join-Path $temp 'config'
    $env:AJQ_CONFIG = Join-Path $temp 'ajq.toml'
    $env:AJQ_CACHE_DIR = Join-Path $temp 'cache'
    $actual = ('[{"id":1,"msg":"refund request"},{"id":2,"msg":"shipping update"}]' | & $ajq --backend mock -c '.[] | select(.msg =~ "refund") | .id' | Out-String)
    if ($actual -cne "1`n") { throw "mock query mismatch: expected 1, got $actual" }
} finally {
    Remove-Item -Recurse -Force -ErrorAction Ignore -LiteralPath $temp
}
Write-Host "WinGet package smoke passed for $Tag"
