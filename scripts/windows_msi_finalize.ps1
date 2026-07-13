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
# PowerShell 7 does not reliably dispatch indexed COM-property assignment with
# `$summary.Property(id) = value`; use IDispatch's explicit property setter.
function Set-MsiSummaryProperty {
    param($Summary, [int]$PropertyId, $Value)
    [void]$Summary.GetType().InvokeMember(
        'Property',
        [System.Reflection.BindingFlags]::SetProperty,
        $null,
        $Summary,
        @($PropertyId, $Value)
    )
}

function Get-MsiSummaryProperty {
    param($Summary, [int]$PropertyId)
    return $Summary.GetType().InvokeMember(
        'Property',
        [System.Reflection.BindingFlags]::GetProperty,
        $null,
        $Summary,
        @($PropertyId)
    )
}

$installer = New-Object -ComObject WindowsInstaller.Installer
$database = $null
$summary = $null
try {
    $database = $installer.OpenDatabase((Resolve-Path -LiteralPath $MsiPath).Path, 1)
    # Commit WiX's database work first: Windows Installer regenerates the
    # package-code summary property during that commit. Persist our stable
    # package code only after it, otherwise the commit overwrites it.
    $database.Commit()
    $summary = $database.SummaryInformation(20)
    $reproducibleTime = [datetime]::SpecifyKind([datetime]'2000-01-01T00:00:00', [DateTimeKind]::Utc)
    Set-MsiSummaryProperty $summary 9 $PackageCode
    Set-MsiSummaryProperty $summary 12 $reproducibleTime
    Set-MsiSummaryProperty $summary 13 $reproducibleTime
    $summary.Persist()
    $persistedPackageCode = Get-MsiSummaryProperty $summary 9
    if ($persistedPackageCode -cne $PackageCode) { throw "failed to persist deterministic MSI package code (got $persistedPackageCode)" }
} finally {
    if ($summary) { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($summary) }
    if ($database) { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($database) }
    [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($installer)
}
