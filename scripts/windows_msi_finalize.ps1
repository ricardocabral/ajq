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
# Invoke the Windows Installer API rather than PowerShell's indexed COM adapter:
# on pwsh 7 the adapter silently fails to persist SummaryInformation.Property.
if (-not ('Ajq.MsiSummaryInfo' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Text;

namespace Ajq {
  [StructLayout(LayoutKind.Sequential)]
  public struct MsiFileTime { public int Low; public int High; }

  public static class MsiSummaryInfo {
    private const uint VT_LPSTR = 30;
    private const uint VT_FILETIME = 64;
    [DllImport("msi.dll", CharSet = CharSet.Unicode)]
    private static extern uint MsiGetSummaryInformation(IntPtr database, string path, uint updateCount, out IntPtr summary);
    [DllImport("msi.dll")]
    private static extern uint MsiSummaryInfoSetProperty(IntPtr summary, uint property, uint dataType, int intValue, IntPtr fileTime, string value);
    [DllImport("msi.dll", CharSet = CharSet.Unicode)]
    private static extern uint MsiSummaryInfoGetProperty(IntPtr summary, uint property, out uint dataType, out int intValue, out MsiFileTime fileTime, StringBuilder value, ref int valueChars);
    [DllImport("msi.dll")]
    private static extern uint MsiSummaryInfoPersist(IntPtr summary);
    [DllImport("msi.dll")]
    private static extern uint MsiCloseHandle(IntPtr handle);

    private static void Check(uint result, string operation) {
      if (result != 0) throw new InvalidOperationException(operation + " failed with Windows Installer error " + result);
    }
    private static IntPtr Open(string path, uint updateCount) {
      IntPtr summary;
      Check(MsiGetSummaryInformation(IntPtr.Zero, path, updateCount, out summary), "opening MSI summary information");
      return summary;
    }
    private static void SetFileTime(IntPtr summary, uint property, DateTime value) {
      long ticks = value.ToFileTimeUtc();
      var fileTime = new MsiFileTime { Low = unchecked((int)ticks), High = unchecked((int)(ticks >> 32)) };
      IntPtr pointer = Marshal.AllocHGlobal(Marshal.SizeOf(typeof(MsiFileTime)));
      try {
        Marshal.StructureToPtr(fileTime, pointer, false);
        Check(MsiSummaryInfoSetProperty(summary, property, VT_FILETIME, 0, pointer, null), "setting MSI summary timestamp " + property);
      } finally { Marshal.FreeHGlobal(pointer); }
    }
    public static void Normalize(string path, string packageCode, DateTime timestamp) {
      IntPtr summary = Open(path, 3);
      try {
        Check(MsiSummaryInfoSetProperty(summary, 9, VT_LPSTR, 0, IntPtr.Zero, packageCode), "setting deterministic MSI package code");
        // WiX 4 emits stable summary timestamps for identical inputs. Rewriting
        // their FILETIME variants invalidates the compound MSI stream on current
        // Windows Installer, so normalize only the generated package code.
        Check(MsiSummaryInfoPersist(summary), "persisting deterministic MSI summary information");
      } finally { MsiCloseHandle(summary); }
    }
    public static string PackageCode(string path) {
      IntPtr summary = Open(path, 0);
      try {
        uint type; int intValue; MsiFileTime fileTime; int chars = 0;
        uint result = MsiSummaryInfoGetProperty(summary, 9, out type, out intValue, out fileTime, null, ref chars);
        if (result != 234 && result != 0) Check(result, "reading MSI package code length");
        var value = new StringBuilder(chars + 1);
        chars = value.Capacity;
        Check(MsiSummaryInfoGetProperty(summary, 9, out type, out intValue, out fileTime, value, ref chars), "reading MSI package code");
        return value.ToString();
      } finally { MsiCloseHandle(summary); }
    }
  }
}
'@
}

$resolvedPath = (Resolve-Path -LiteralPath $MsiPath).Path
$reproducibleTime = [datetime]::SpecifyKind([datetime]'2000-01-01T00:00:00', [DateTimeKind]::Utc)
[Ajq.MsiSummaryInfo]::Normalize($resolvedPath, $PackageCode, $reproducibleTime)
$persistedPackageCode = [Ajq.MsiSummaryInfo]::PackageCode($resolvedPath)
if ($persistedPackageCode -cne $PackageCode) { throw "failed to persist deterministic MSI package code (got $persistedPackageCode)" }
