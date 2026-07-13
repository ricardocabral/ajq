$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent $PSScriptRoot
$helper = Join-Path $repoRoot 'scripts/winget_release_preflight.ps1'
$temp = Join-Path ([System.IO.Path]::GetTempPath()) ("ajq-winget-preflight-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $temp | Out-Null
try {
    $gh = if ($IsWindows) { Join-Path $temp 'gh.cmd' } else { Join-Path $temp 'gh' }
    if ($IsWindows) {
        @'
@echo off
if "%WINGET_PACKAGE_STATE%"=="present" (echo {} & exit /b 0)
if "%WINGET_PACKAGE_STATE%"=="absent" (echo HTTP 404: Not Found & exit /b 1)
echo HTTP 500: synthetic service failure
exit /b 1
'@ | Set-Content -LiteralPath $gh
    } else {
        @'
#!/bin/sh
case "$WINGET_PACKAGE_STATE" in
  present) echo '{}'; exit 0 ;;
  absent) echo 'HTTP 404: Not Found'; exit 1 ;;
  *) echo 'HTTP 500: synthetic service failure'; exit 1 ;;
esac
'@ | Set-Content -LiteralPath $gh
        & chmod +x $gh
    }
    $env:GH_BIN = $gh

    function Invoke-Preflight([string]$token, [string]$tag, [object[]]$assets, [string]$packageState, [bool]$expectSuccess) {
        $release = @{ tagName = $tag; assets = @($assets | ForEach-Object { @{ name = $_ } }) } | ConvertTo-Json -Depth 3
        $json = Join-Path $temp 'release.json'
        $output = Join-Path $temp 'output.txt'
        Set-Content -NoNewline -LiteralPath $json -Value $release
        Remove-Item -ErrorAction Ignore -LiteralPath $output
        $env:WINGET_TOKEN = $token
        $env:WINGET_PACKAGE_STATE = $packageState
        try {
            & $helper -Tag $tag -ReleaseJsonPath $json -OutputPath $output
            if (-not $expectSuccess) { throw 'expected preflight failure' }
            return Get-Content -Raw -LiteralPath $output
        } catch {
            if ($expectSuccess) { throw }
            if ($_.Exception.Message -match [regex]::Escape($token) -and $token) { throw 'credential leaked in diagnostic' }
            return $_.Exception.Message
        }
    }

    $msi = 'ajq_1.2.3_Windows_x86_64.msi'
    $zip = 'ajq_1.2.3_Windows_x86_64.zip'
    $message = Invoke-Preflight '' 'v1.2.3' @($msi) 'present' $false
    if ($message -ne 'missing required secret: WINGET_TOKEN') { throw "unexpected missing-token diagnostic: $message" }
    $message = Invoke-Preflight 'synthetic-token' 'not-a-tag' @($msi) 'present' $false
    if ($message -notmatch 'WinGet preflight requires a vX.Y.Z tag') { throw "unexpected tag diagnostic: $message" }
    $message = Invoke-Preflight 'synthetic-token' 'v1.2.3' @($msi) 'absent' $false
    if ($message -notmatch 'bootstrap required.*manual submission') { throw "missing-package bootstrap diagnostic was not actionable: $message" }
    $message = Invoke-Preflight 'synthetic-token' 'v1.2.3' @($msi) 'failure' $false
    if ($message -notmatch 'could not verify existing RicardoCabral.ajq') { throw "API failure leaked or was not diagnosed: $message" }
    foreach ($assets in @(@(), @($zip), @($msi, $msi), @('ajq_1.2.3_windows_x86_64.msi'))) {
        $message = Invoke-Preflight 'synthetic-token' 'v1.2.3' $assets 'present' $false
        if ($message -notmatch 'requires exactly one asset ajq_1.2.3_Windows_x86_64.msi') { throw "MSI selector accepted invalid assets: $message" }
    }
    $output = Invoke-Preflight 'synthetic-token' 'v1.2.3' @($msi, $zip) 'present' $true
    $outputLines = @($output -split '\r?\n' | ForEach-Object { $_.TrimEnd("`r") })
    foreach ($line in @('version=1.2.3', 'asset=ajq_1.2.3_Windows_x86_64.msi', 'regex=^ajq_1\.2\.3_Windows_x86_64\.msi$', 'identifier=RicardoCabral.ajq', 'installer_type=wix', 'architecture=x64')) {
        if ($outputLines -notcontains $line) { throw "preflight omitted MSI manifest contract ${line}: $output" }
    }
    Write-Host 'winget release preflight tests passed'
} finally {
    Remove-Item -Recurse -Force -ErrorAction Ignore -LiteralPath $temp
    'WINGET_TOKEN', 'WINGET_PACKAGE_STATE', 'GH_BIN' | ForEach-Object { Remove-Item "Env:$_" -ErrorAction Ignore }
}
