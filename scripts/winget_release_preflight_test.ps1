$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent $PSScriptRoot
$helper = Join-Path $repoRoot 'scripts/winget_release_preflight.ps1'
$temp = Join-Path ([System.IO.Path]::GetTempPath()) ("ajq-winget-preflight-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $temp | Out-Null
try {
    function Invoke-Preflight([string]$token, [string]$tag, [object[]]$assets, [bool]$expectSuccess) {
        $release = @{ tagName = $tag; assets = @($assets | ForEach-Object { @{ name = $_ } }) } | ConvertTo-Json -Depth 3
        $json = Join-Path $temp 'release.json'
        $output = Join-Path $temp 'output.txt'
        Set-Content -NoNewline -LiteralPath $json -Value $release
        Remove-Item -ErrorAction Ignore -LiteralPath $output
        $env:WINGET_TOKEN = $token
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

    $message = Invoke-Preflight '' 'v1.2.3' @('ajq_1.2.3_Windows_x86_64.zip') $false
    if ($message -ne 'missing required secret: WINGET_TOKEN') { throw "unexpected missing-token diagnostic: $message" }
    $message = Invoke-Preflight 'synthetic-token' 'not-a-tag' @('ajq_1.2.3_Windows_x86_64.zip') $false
    if ($message -notmatch 'WinGet preflight requires a vX.Y.Z tag') { throw "unexpected tag diagnostic: $message" }
    $message = Invoke-Preflight 'synthetic-token' 'v1.2.3' @() $false
    if ($message -notmatch 'Ricardocabral.ajq tag v1.2.3 requires exactly one asset ajq_1.2.3_Windows_x86_64.zip') { throw "unexpected no-asset diagnostic: $message" }
    $message = Invoke-Preflight 'synthetic-token' 'v1.2.3' @('ajq_1.2.3_Windows_x86_64.zip', 'ajq_1.2.3_Windows_x86_64.zip') $false
    if ($message -notmatch 'requires exactly one asset') { throw "unexpected duplicate-asset diagnostic: $message" }
    $output = Invoke-Preflight 'synthetic-token' 'v1.2.3' @('ajq_1.2.3_Windows_x86_64.zip') $true
    $outputLines = @($output -split '\r?\n' | ForEach-Object { $_.TrimEnd("`r") })
    if ($outputLines -notcontains 'version=1.2.3' -or $outputLines -notcontains 'regex=^ajq_1\.2\.3_Windows_x86_64\.zip$') { throw "unexpected preflight output: $output" }
    Write-Host 'winget release preflight tests passed'
} finally {
    Remove-Item -Recurse -Force -ErrorAction Ignore -LiteralPath $temp
    Remove-Item Env:WINGET_TOKEN -ErrorAction Ignore
}
