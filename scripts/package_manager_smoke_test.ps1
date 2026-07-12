$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent $PSScriptRoot
$smoke = Join-Path $repoRoot 'scripts/package_manager_smoke.ps1'
$temp = Join-Path ([System.IO.Path]::GetTempPath()) ("ajq-package-smoke-test-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $temp | Out-Null
try {
    $winget = Join-Path $temp 'winget.ps1'
    @'
param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Args)
Add-Content -LiteralPath $env:WINGET_LOG -Value ($Args -join ' ')
if ($Args[0] -eq 'list') { Write-Output "Ricardocabral.ajq $env:WINGET_VERSION" }
'@ | Set-Content -LiteralPath $winget
    $ajq = Join-Path $temp 'ajq.ps1'
    @'
param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Args)
if ($Args[0] -eq '--version') { Write-Output "ajq v$env:AJQ_VERSION" } else { $input | Out-Null; Write-Output $env:QUERY_OUTPUT }
'@ | Set-Content -LiteralPath $ajq

    function Invoke-Smoke([string]$mode) {
        $env:WINGET_LOG = Join-Path $temp 'winget.log'
        $env:WINGET_BIN = if ($mode -eq 'missing') { Join-Path $temp 'missing-winget.exe' } else { $winget }
        $env:AJQ_PACKAGE_EXECUTABLE = $ajq
        $env:WINGET_VERSION = if ($mode -eq 'version') { '9.9.9' } else { '1.2.3' }
        $env:AJQ_VERSION = '1.2.3'
        $env:QUERY_OUTPUT = if ($mode -eq 'query') { '2' } else { '1' }
        try {
            & $smoke -Channel winget -Tag v1.2.3
            return @{ Success = $true; Output = '' }
        } catch {
            return @{ Success = $false; Output = $_.Exception.Message }
        }
    }

    $result = Invoke-Smoke 'success'
    if (-not $result.Success) { throw "smoke success failed: $($result.Output)" }
    foreach ($command in @(
        'uninstall --id Ricardocabral.ajq --exact --silent',
        'source update',
        'install --id Ricardocabral.ajq --exact --version 1.2.3 --source winget --accept-package-agreements --accept-source-agreements --silent'
    )) {
        if (-not (Select-String -LiteralPath $env:WINGET_LOG -SimpleMatch $command -Quiet)) { throw "missing winget construction: $command" }
    }
    $result = Invoke-Smoke 'version'
    if ($result.Success -or $result.Output -notmatch 'installed WinGet package version mismatch') { throw "version mismatch was not rejected: $($result.Output)" }
    $result = Invoke-Smoke 'query'
    if ($result.Success -or $result.Output -notmatch 'mock query mismatch') { throw "query mismatch was not rejected: $($result.Output)" }
    $result = Invoke-Smoke 'missing'
    if ($result.Success -or $result.Output -ne 'required tool not found: winget') { throw "missing tool was not rejected: $($result.Output)" }
    Write-Host 'package-manager smoke PowerShell tests passed'
} finally {
    Remove-Item -Recurse -Force -ErrorAction Ignore -LiteralPath $temp
    'WINGET_LOG', 'WINGET_BIN', 'AJQ_PACKAGE_EXECUTABLE', 'WINGET_VERSION', 'AJQ_VERSION', 'QUERY_OUTPUT' | ForEach-Object { Remove-Item "Env:$_" -ErrorAction Ignore }
}
