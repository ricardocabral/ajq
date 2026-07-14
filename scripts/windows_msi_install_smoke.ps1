[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Tag,
    [Parameter(Mandatory = $true)][string]$MsiPath,
    [string]$ExpectedSha256,
    [string]$ExpectedBinarySha256
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$contract = @{}
& (Join-Path $PSScriptRoot 'windows_msi_contract.ps1') -Tag $Tag | ForEach-Object {
    $name, $value = $_ -split '=', 2
    $contract[$name] = $value
}
if ((Split-Path -Leaf $MsiPath) -cne $contract.msi_asset) { throw "MSI filename must be exactly $($contract.msi_asset)" }
if (-not (Test-Path -LiteralPath $MsiPath -PathType Leaf)) { throw "MSI not found: $MsiPath" }
# msiexec receives an absolute, quoted package path; unlike COM database APIs,
# it does not reliably resolve a workflow-relative MSI path.
$MsiPath = (Resolve-Path -LiteralPath $MsiPath).Path
$hashBefore = (Get-FileHash -LiteralPath $MsiPath -Algorithm SHA256).Hash.ToUpperInvariant()
if ($ExpectedSha256 -and $hashBefore -cne $ExpectedSha256.ToUpperInvariant()) { throw 'MSI SHA-256 does not match the recorded draft/build hash' }

function Get-MsiProperty([string]$Path, [string]$Name) {
    $installer = New-Object -ComObject WindowsInstaller.Installer
    $database = $null; $view = $null; $record = $null
    try {
        $database = $installer.OpenDatabase($Path, 0)
        $view = $database.OpenView("SELECT `Value` FROM `Property` WHERE `Property`='$Name'")
        $view.Execute()
        $record = $view.Fetch()
        if ($null -eq $record) { throw "MSI property missing: $Name" }
        return $record.StringData(1)
    } finally {
        foreach ($com in @($record, $view, $database, $installer)) {
            if ($null -ne $com -and [Runtime.InteropServices.Marshal]::IsComObject($com)) { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($com) }
        }
    }
}

if ((Get-MsiProperty $MsiPath 'ProductVersion') -cne $contract.version) { throw "MSI ProductVersion does not match $($contract.version)" }
if ((Get-MsiProperty $MsiPath 'ProductCode') -cne $contract.product_code) { throw "MSI ProductCode does not match deterministic contract $($contract.product_code)" }
$installDirectory = Join-Path $env:LOCALAPPDATA 'Programs\ajq'
$executable = Join-Path $installDirectory 'ajq.exe'
if (Test-Path -LiteralPath $installDirectory) { throw "refusing non-isolated MSI smoke: install directory already exists: $installDirectory" }

function Invoke-MsiExec([string[]]$Arguments, [string]$Description) {
    $process = Start-Process -FilePath msiexec.exe -ArgumentList $Arguments -Wait -PassThru -NoNewWindow
    if ($process.ExitCode -notin @(0, 3010)) { throw "$Description failed with Windows Installer exit $($process.ExitCode)" }
}
function Get-ByteEvidence([byte[]]$Bytes) {
    $sha256 = [Security.Cryptography.SHA256]::HashData($Bytes)
    return "bytes=$($Bytes.Length) sha256=$([Convert]::ToHexString($sha256))"
}
function Assert-ExactOutput([string]$Path, [string[]]$Arguments, [string]$StandardInput, [string]$Expected, [string]$Description) {
    # ArgumentList preserves each argv element exactly. Record only byte lengths
    # and hashes so a failed CI run distinguishes invocation/input/capture faults
    # without exposing arbitrary environment-derived command content.
    $psi = [Diagnostics.ProcessStartInfo]::new()
    $psi.FileName = $Path
    $psi.UseShellExecute = $false; $psi.RedirectStandardInput = $true; $psi.RedirectStandardOutput = $true; $psi.RedirectStandardError = $true
    foreach ($argument in $Arguments) { [void]$psi.ArgumentList.Add($argument) }
    $process = [Diagnostics.Process]::new(); $process.StartInfo = $psi; [void]$process.Start()
    $process.StandardInput.Write($StandardInput); $process.StandardInput.Close()
    $stderrTask = $process.StandardError.ReadToEndAsync()
    $memory = [IO.MemoryStream]::new(); $process.StandardOutput.BaseStream.CopyTo($memory); $process.WaitForExit()
    $stderr = $stderrTask.GetAwaiter().GetResult()
    $actualBytes = $memory.ToArray()
    $expectedBytes = [Text.Encoding]::UTF8.GetBytes($Expected)
    $argumentEvidence = ($Arguments | ForEach-Object { Get-ByteEvidence ([Text.Encoding]::UTF8.GetBytes($_)) }) -join ', '
    $evidence = "path=$Path; argv=[$argumentEvidence]; stdin=$(Get-ByteEvidence ([Text.Encoding]::UTF8.GetBytes($StandardInput))); stdout=$(Get-ByteEvidence $actualBytes); stderr=$(Get-ByteEvidence ([Text.Encoding]::UTF8.GetBytes($stderr)))"
    if ($process.ExitCode -ne 0) { throw "$Description failed (exit=$($process.ExitCode); $evidence)" }
    $actual = [Convert]::ToBase64String($actualBytes)
    $expected = [Convert]::ToBase64String($expectedBytes)
    if ($actual -cne $expected) { throw "$Description did not produce exact expected bytes ($evidence; actual base64: $actual; expected base64: $expected)" }
    Write-Host "$Description evidence: $evidence"
    return $actual
}

$temp = Join-Path ([IO.Path]::GetTempPath()) ("ajq-msi-smoke-" + [guid]::NewGuid())
$installed = $false
try {
    Invoke-MsiExec @('/i', $MsiPath, '/qn', '/norestart') 'silent MSI install'
    $installed = $true
    if (-not (Test-Path -LiteralPath $executable -PathType Leaf)) { throw "MSI did not install ajq at $executable" }
    $installedBinaryHash = (Get-FileHash -LiteralPath $executable -Algorithm SHA256).Hash.ToUpperInvariant()
    if ($ExpectedBinarySha256 -and $installedBinaryHash -cne $ExpectedBinarySha256.ToUpperInvariant()) { throw "installed ajq.exe SHA-256 does not match WiX source binary (installed=$installedBinaryHash expected=$($ExpectedBinarySha256.ToUpperInvariant()))" }
    Write-Output "Windows MSI installed ajq.exe SHA-256: $installedBinaryHash"
    $userPath = (Get-ItemProperty -LiteralPath 'HKCU:\Environment' -Name Path -ErrorAction SilentlyContinue).Path
    if ($userPath -notmatch [regex]::Escape($installDirectory)) { throw 'MSI did not own the per-user PATH entry' }
    New-Item -ItemType Directory -Path $temp | Out-Null
    $env:HOME = Join-Path $temp 'home'; $env:XDG_CONFIG_HOME = Join-Path $temp 'config'; $env:AJQ_CONFIG = Join-Path $temp 'ajq.toml'; $env:AJQ_CACHE_DIR = Join-Path $temp 'cache'
    New-Item -ItemType File -Path $env:AJQ_CONFIG | Out-Null
    $versionEvidence = Assert-ExactOutput $executable @('--version') '' "ajq $($contract.version)`n" 'installed ajq version'
    $mockEvidence = Assert-ExactOutput $executable @('--backend', 'mock', '-c', '.[] | select(.msg =~ "refund") | .id') "[{`"id`":1,`"msg`":`"refund request`"},{`"id`":2,`"msg`":`"shipping update`"}]`n" "1`n" 'installed mock query'
    if ((Get-FileHash -LiteralPath $MsiPath -Algorithm SHA256).Hash.ToUpperInvariant() -cne $hashBefore) { throw 'MSI changed after installation' }
    Write-Output "Windows MSI SHA-256: $hashBefore"
    Write-Output "Windows MSI version evidence base64: $versionEvidence"
    Write-Output "Windows MSI mock stdout base64: $mockEvidence"
} finally {
    if ($installed) { Invoke-MsiExec @('/x', $MsiPath, '/qn', '/norestart') 'silent MSI uninstall' }
    Remove-Item -Recurse -Force -ErrorAction Ignore -LiteralPath $temp
    if (Test-Path -LiteralPath $installDirectory) { throw "MSI uninstall left installation directory: $installDirectory" }
    $userPath = (Get-ItemProperty -LiteralPath 'HKCU:\Environment' -Name Path -ErrorAction SilentlyContinue).Path
    if ($userPath -match [regex]::Escape($installDirectory)) { throw 'MSI uninstall left its per-user PATH entry behind' }
}
Write-Output "Windows MSI install smoke passed for $Tag"
