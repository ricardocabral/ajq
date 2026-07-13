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
$global:LASTEXITCODE = 0
if ($env:WINGET_FAIL_OPERATION -and $Args[0] -eq $env:WINGET_FAIL_OPERATION) { $global:LASTEXITCODE = 9; return }
if ($Args[0] -eq 'list') {
    if ($env:WINGET_ABSENT -eq '1') { Write-Output 'No installed package found matching input criteria.' }
    else { Write-Output "RicardoCabral.ajq $env:WINGET_VERSION" }
}
'@ | Set-Content -LiteralPath $winget
    $ajq = Join-Path $temp 'ajq.ps1'
    @'
param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Args)
if (-not (Test-Path -LiteralPath $env:AJQ_CONFIG -PathType Leaf)) { exit 42 }
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
        Remove-Item -LiteralPath $env:WINGET_LOG -Force -ErrorAction Ignore
        $env:WINGET_BIN = if ($mode -eq 'missing') { Join-Path $temp 'missing-winget.exe' } else { $winget }
        $env:AJQ_PACKAGE_EXECUTABLE = if ($mode -eq 'missing-executable') { Join-Path $temp 'missing-ajq.exe' } else { $ajq }
        $env:WINGET_VERSION = if ($mode -eq 'version') { '9.9.9' } else { '1.2.3' }
        $env:WINGET_FAIL_OPERATION = if ($mode -like 'fail-*') { $mode.Substring(5) } else { '' }
        $env:WINGET_ABSENT = if ($mode -eq 'absent') { '1' } else { '' }
        $env:AJQ_VERSION_OUTPUT = if ($mode -eq 'version-output') { "ajq 1.2.3 suffix`n" } else { "ajq 1.2.3`n" }
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
    if ($result.Output -notmatch 'WinGet installed version: ajq 1\.2\.3') { throw 'success evidence omitted exact version' }
    if ($result.Output -notmatch 'WinGet mock stdout base64: MQo=') { throw 'success evidence omitted mock stdout bytes' }
    foreach ($command in @(
        'uninstall --id RicardoCabral.ajq --exact --silent',
        'source update',
        'install --id RicardoCabral.ajq --exact --version 1.2.3 --source winget --accept-package-agreements --accept-source-agreements --silent'
    )) {
        if (-not (Select-String -LiteralPath $env:WINGET_LOG -SimpleMatch $command -Quiet)) { throw "missing winget construction: $command" }
    }
    $result = Invoke-Smoke 'absent'
    if ($result.Success -or $result.Output -notmatch 'installed WinGet package version mismatch') { throw "absent package was not distinguished from an uninstall failure: $($result.Output)" }
    if (-not (Select-String -LiteralPath $env:WINGET_LOG -SimpleMatch 'uninstall --id RicardoCabral.ajq --exact --silent' -Quiet)) { throw 'post-install absent-package assertion did not clean up the package' }
    foreach ($operation in @('uninstall', 'source', 'install', 'list')) {
        $result = Invoke-Smoke "fail-$operation"
        if ($result.Success -or $result.Output -notmatch "WinGet .*${operation}.*failed \(exit 9\)") { throw "$operation failure was not rejected: $($result.Output)" }
    }
    $result = Invoke-Smoke 'version'
    if ($result.Success -or $result.Output -notmatch 'installed WinGet package version mismatch') { throw "version mismatch was not rejected: $($result.Output)" }
    if (-not (Select-String -LiteralPath $env:WINGET_LOG -SimpleMatch 'uninstall --id RicardoCabral.ajq --exact --silent' -Quiet)) { throw 'post-install version mismatch did not clean up the package' }
    $result = Invoke-Smoke 'missing-executable'
    if ($result.Success -or $result.Output -notmatch 'installed WinGet executable not found') { throw "missing executable was not rejected: $($result.Output)" }
    if (-not (Select-String -LiteralPath $env:WINGET_LOG -SimpleMatch 'uninstall --id RicardoCabral.ajq --exact --silent' -Quiet)) { throw 'missing installed executable did not clean up the package' }
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
    'WINGET_LOG', 'WINGET_BIN', 'AJQ_PACKAGE_EXECUTABLE', 'WINGET_VERSION', 'WINGET_FAIL_OPERATION', 'WINGET_ABSENT', 'AJQ_VERSION_OUTPUT', 'QUERY_OUTPUT' | ForEach-Object { Remove-Item "Env:$_" -ErrorAction Ignore }
}
