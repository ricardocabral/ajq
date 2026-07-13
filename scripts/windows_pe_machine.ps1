[CmdletBinding()]
param([Parameter(Mandatory = $true)][string]$BinaryPath)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$bytes = [IO.File]::ReadAllBytes($BinaryPath)
if ($bytes.Length -lt 0x40 -or $bytes[0] -ne 0x4d -or $bytes[1] -ne 0x5a) {
    throw 'release binary is not a DOS MZ executable'
}
$peOffset = [BitConverter]::ToInt32($bytes, 0x3c)
if ($peOffset -lt 0 -or $peOffset + 6 -gt $bytes.Length) { throw 'release binary has an invalid PE header offset' }
if ($bytes[$peOffset] -ne 0x50 -or $bytes[$peOffset + 1] -ne 0x45 -or $bytes[$peOffset + 2] -ne 0 -or $bytes[$peOffset + 3] -ne 0) {
    throw 'release binary does not contain a PE signature'
}
$machine = [BitConverter]::ToUInt16($bytes, $peOffset + 4)
if ($machine -ne 0x8664) { throw ('release binary PE machine is 0x{0:X4}, expected AMD64 0x8664' -f $machine) }
