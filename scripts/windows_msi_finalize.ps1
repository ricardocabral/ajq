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

# WiX emits a valid Installer summary stream, including its PackageCode. Do
# not rewrite that stream: Windows Installer rejects an MSI whose compound
# summary stream was changed after WiX bound the internal cabinet. The release
# workflow's two-build SHA-256 comparison is the authoritative reproducibility
# gate; this helper deliberately validates inputs without mutating the MSI.
Write-Verbose "MSI summary stream left intact; release retry reproducibility is verified by workflow SHA-256 comparison."
