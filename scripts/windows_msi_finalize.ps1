[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$MsiPath,
    [Parameter(Mandatory = $true)][string]$PackageCode
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if (-not (Test-Path -LiteralPath $MsiPath -PathType Leaf)) { throw "MSI not found: $MsiPath" }
if ($PackageCode -notmatch '^\{[0-9A-F]{8}(-[0-9A-F]{4}){3}-[0-9A-F]{12}\}$') {
    throw 'PackageCode must be an upper-case braced GUID'
}

# WiX generates a new package code and current summary timestamps for each
# build. Normalize those mutable summary properties after every build so the
# same verified input archive yields the same unsigned MSI bytes on retries.
$installer = New-Object -ComObject WindowsInstaller.Installer
$database = $null
$summary = $null
try {
    $database = $installer.OpenDatabase((Resolve-Path -LiteralPath $MsiPath).Path, 1)
    $summary = $database.SummaryInformation(20)
    $reproducibleTime = [datetime]::SpecifyKind([datetime]'2000-01-01T00:00:00', [DateTimeKind]::Utc)
    $summary.Property(9) = $PackageCode
    $summary.Property(12) = $reproducibleTime
    $summary.Property(13) = $reproducibleTime
    $summary.Persist()
    $database.Commit()
    if ($summary.Property(9) -cne $PackageCode) { throw 'failed to persist deterministic MSI package code' }
} finally {
    if ($summary) { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($summary) }
    if ($database) { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($database) }
    [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($installer)
}
