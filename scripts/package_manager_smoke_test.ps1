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
$bytes = if ($Args[0] -eq '--version') {
    [System.Text.Encoding]::UTF8.GetBytes($env:AJQ_VERSION_OUTPUT)
} else {
    $input | Out-Null
    [System.Text.Encoding]::UTF8.GetBytes($env:QUERY_OUTPUT)
}
[Console]::OpenStandardOutput().Write($bytes, 0, $bytes.Length)
'@ | Set-Content -LiteralPath $ajq

    function Invoke-Smoke([string]$mode) {
        $env:WINGET_LOG = Join-Path $temp 'winget.log'
        $env:WINGET_BIN = if ($mode -eq 'missing') { Join-Path $temp 'missing-winget.exe' } else { $winget }
        $env:AJQ_PACKAGE_EXECUTABLE = $ajq
        $env:WINGET_VERSION = if ($mode -eq 'version') { '9.9.9' } else { '1.2.3' }
        $env:AJQ_VERSION_OUTPUT = if ($mode -eq 'version-output') { "ajq v1.2.3 suffix`n" } else { "ajq v1.2.3`n" }
        $env:QUERY_OUTPUT = switch ($mode) {
            'query' { "2`n" }
            'query-crlf' { "1`r`n" }
            'query-extra-byte' { "1`n2`n" }
            default { "1`n" }
        }
        try {
            $output = (& $smoke -Channel winget -Tag v1.2.3 2>&1 | Out-String)
            return @{ Success = $true; Output = $output }
        } catch {
            return @{ Success = $false; Output = $_.Exception.Message }
        }
    }

    $result = Invoke-Smoke 'success'
    if (-not $result.Success) { throw "smoke success failed: $($result.Output)" }
    if ($result.Output -notmatch 'WinGet installed version: ajq v1\.2\.3') { throw 'success evidence omitted exact version' }
    if ($result.Output -notmatch 'WinGet mock stdout base64: MQo=') { throw 'success evidence omitted mock stdout bytes' }
    foreach ($command in @(
        'uninstall --id Ricardocabral.ajq --exact --silent',
        'source update',
        'install --id Ricardocabral.ajq --exact --version 1.2.3 --source winget --accept-package-agreements --accept-source-agreements --silent'
    )) {
        if (-not (Select-String -LiteralPath $env:WINGET_LOG -SimpleMatch $command -Quiet)) { throw "missing winget construction: $command" }
    }
    $result = Invoke-Smoke 'version'
    if ($result.Success -or $result.Output -notmatch 'installed WinGet package version mismatch') { throw "version mismatch was not rejected: $($result.Output)" }
    $result = Invoke-Smoke 'version-output'
    if ($result.Success -or $result.Output -notmatch 'ajq version mismatch') { throw "executable version suffix was not rejected: $($result.Output)" }
    foreach ($mode in @('query', 'query-crlf', 'query-extra-byte')) {
        $result = Invoke-Smoke $mode
        if ($result.Success -or $result.Output -notmatch 'mock query mismatch') { throw "$mode was not rejected: $($result.Output)" }
    }
    $result = Invoke-Smoke 'missing'
    if ($result.Success -or $result.Output -ne 'required tool not found: winget') { throw "missing tool was not rejected: $($result.Output)" }
    Write-Host 'package-manager smoke PowerShell tests passed'
} finally {
    Remove-Item -Recurse -Force -ErrorAction Ignore -LiteralPath $temp
    'WINGET_LOG', 'WINGET_BIN', 'AJQ_PACKAGE_EXECUTABLE', 'WINGET_VERSION', 'AJQ_VERSION_OUTPUT', 'QUERY_OUTPUT' | ForEach-Object { Remove-Item "Env:$_" -ErrorAction Ignore }
}
