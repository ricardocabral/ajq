[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][ValidateSet('winget')][string]$Channel,
    [Parameter(Mandatory = $true)][string]$Tag
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if ($Tag -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+$') { throw "tag must be vX.Y.Z, got $Tag" }
$version = $Tag.Substring(1)

function Invoke-ProgramToFile([string]$Path, [string[]]$Arguments, [string]$Input, [string]$OutputPath) {
    $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
    $startInfo.UseShellExecute = $false
    $startInfo.RedirectStandardInput = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    if ([System.IO.Path]::GetExtension($Path) -eq '.ps1') {
        $startInfo.FileName = (Get-Command pwsh -CommandType Application -ErrorAction Stop | Select-Object -First 1).Source
        [void]$startInfo.ArgumentList.Add('-NoProfile')
        [void]$startInfo.ArgumentList.Add('-File')
        [void]$startInfo.ArgumentList.Add($Path)
    } else {
        $startInfo.FileName = $Path
    }
    foreach ($argument in $Arguments) { [void]$startInfo.ArgumentList.Add($argument) }

    $process = [System.Diagnostics.Process]::new()
    $process.StartInfo = $startInfo
    [void]$process.Start()
    $process.StandardInput.Write($Input)
    $process.StandardInput.Close()
    $stderrTask = $process.StandardError.ReadToEndAsync()
    $file = [System.IO.File]::Open($OutputPath, [System.IO.FileMode]::Create, [System.IO.FileAccess]::Write)
    try {
        $process.StandardOutput.BaseStream.CopyTo($file)
    } finally {
        $file.Dispose()
    }
    $process.WaitForExit()
    $stderr = $stderrTask.GetAwaiter().GetResult()
    if ($process.ExitCode -ne 0) { throw "command failed ($($process.ExitCode)): $stderr" }
}

function Assert-ExactBytes([string]$Path, [string]$Expected, [string]$Description) {
    $actualBase64 = [Convert]::ToBase64String([System.IO.File]::ReadAllBytes($Path))
    $expectedBase64 = [Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes($Expected))
    if ($actualBase64 -cne $expectedBase64) { throw "$Description mismatch: expected exact output bytes" }
    return $actualBase64
}

$wingetPath = $env:WINGET_BIN
if (-not $wingetPath) {
    $winget = Get-Command winget -CommandType Application -ErrorAction SilentlyContinue
    if (-not $winget) { throw 'required tool not found: winget' }
    $wingetPath = $winget.Source
}
if (-not (Test-Path -LiteralPath $wingetPath -PathType Leaf)) { throw 'required tool not found: winget' }

function Invoke-WinGet([string]$Operation, [string[]]$Arguments) {
    $output = (& $wingetPath @Arguments 2>&1 | Out-String)
    # A real winget executable always sets LASTEXITCODE. Treat its absence as a
    # successful PowerShell-script test double, rather than relying on Stop.
    $exitCode = Get-Variable -Name LASTEXITCODE -Scope Global -ValueOnly -ErrorAction SilentlyContinue
    if ($null -eq $exitCode) { $exitCode = 0 }
    if ($exitCode -ne 0) {
        throw "WinGet $Operation failed (exit $exitCode): $output"
    }
    return $output
}

$packageArguments = @('list', '--id', 'RicardoCabral.ajq', '--exact')
$existing = Invoke-WinGet 'list before cleanup' $packageArguments
if ($existing -notmatch '(?im)no (installed )?package found matching input criteria') {
    [void](Invoke-WinGet 'uninstall' @('uninstall', '--id', 'RicardoCabral.ajq', '--exact', '--silent'))
}
[void](Invoke-WinGet 'source update' @('source', 'update'))
[void](Invoke-WinGet 'install' @('install', '--id', 'RicardoCabral.ajq', '--exact', '--version', $version, '--source', 'winget', '--accept-package-agreements', '--accept-source-agreements', '--silent'))
$installed = Invoke-WinGet 'list after install' $packageArguments
if ($installed -notmatch "(?m)\b$([regex]::Escape($version))\b") { throw "installed WinGet package version mismatch: expected $version" }

# Portable WinGet aliases live here; never use an arbitrary older ajq on PATH.
$ajq = if ($env:AJQ_PACKAGE_EXECUTABLE) { $env:AJQ_PACKAGE_EXECUTABLE } else { Join-Path $env:LOCALAPPDATA 'Microsoft\WinGet\Links\ajq.exe' }
if (-not (Test-Path -LiteralPath $ajq -PathType Leaf)) { throw "installed WinGet executable not found: $ajq" }
$temp = Join-Path ([System.IO.Path]::GetTempPath()) ("ajq-package-smoke-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $temp | Out-Null
New-Item -ItemType File -Path (Join-Path $temp 'ajq.toml') | Out-Null
try {
    $env:HOME = Join-Path $temp 'home'
    $env:XDG_CONFIG_HOME = Join-Path $temp 'config'
    $env:AJQ_CONFIG = Join-Path $temp 'ajq.toml'
    $env:AJQ_CACHE_DIR = Join-Path $temp 'cache'

    $versionFile = Join-Path $temp 'version'
    Invoke-ProgramToFile $ajq @('--version') '' $versionFile
    $versionEvidence = Assert-ExactBytes $versionFile "ajq $version`n" 'ajq version'
    Write-Output "WinGet installed version: ajq $version"

    $mockFile = Join-Path $temp 'mock-output'
    Invoke-ProgramToFile $ajq @('--backend', 'mock', '-c', '.[] | select(.msg =~ "refund") | .id') "[{`"id`":1,`"msg`":`"refund request`"},{`"id`":2,`"msg`":`"shipping update`"}]`n" $mockFile
    $mockEvidence = Assert-ExactBytes $mockFile "1`n" 'mock query'
    Write-Output "WinGet mock stdout base64: $mockEvidence"
} finally {
    Remove-Item -Recurse -Force -ErrorAction Ignore -LiteralPath $temp
}
Write-Output "WinGet package smoke passed for $Tag"
