$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent $PSScriptRoot
$contract = Join-Path $repoRoot 'scripts/windows_msi_contract.ps1'
$wix = Get-Content -Raw -LiteralPath (Join-Path $repoRoot 'build/windows/ajq.wxs')
$workflow = Get-Content -Raw -LiteralPath (Join-Path $repoRoot '.github/workflows/release.yml')
$goreleaser = Get-Content -Raw -LiteralPath (Join-Path $repoRoot '.goreleaser.yaml')
$msiFinalizer = Get-Content -Raw -LiteralPath (Join-Path $repoRoot 'scripts/windows_msi_finalize.ps1')
$peMachine = Join-Path $repoRoot 'scripts/windows_pe_machine.ps1'

function Get-Contract([string]$Tag) {
    $result = @{}
    & $contract -Tag $Tag | ForEach-Object {
        $name, $value = $_ -split '=', 2
        $result[$name] = $value
    }
    return $result
}

$peFixture = Join-Path ([IO.Path]::GetTempPath()) ("ajq-pe-machine-" + [guid]::NewGuid())
try {
    $bytes = [byte[]]::new(0x100)
    $bytes[0] = 0x4d; $bytes[1] = 0x5a
    [Array]::Copy([BitConverter]::GetBytes(0x80), 0, $bytes, 0x3c, 4)
    $bytes[0x80] = 0x50; $bytes[0x81] = 0x45
    [Array]::Copy([BitConverter]::GetBytes([uint16]0x8664), 0, $bytes, 0x84, 2)
    [IO.File]::WriteAllBytes($peFixture, $bytes)
    & $peMachine -BinaryPath $peFixture
    [Array]::Copy([BitConverter]::GetBytes([uint16]0x014c), 0, $bytes, 0x84, 2)
    [IO.File]::WriteAllBytes($peFixture, $bytes)
    try {
        & $peMachine -BinaryPath $peFixture
        throw 'x86 PE fixture unexpectedly passed AMD64 validation'
    } catch {
        if ($_.Exception.Message -notmatch 'expected AMD64 0x8664') { throw }
    }
} finally {
    Remove-Item -LiteralPath $peFixture -Force -ErrorAction Ignore
}

$first = Get-Contract 'v0.0.7'
if ($first.version -ne '0.0.7' -or $first.product_code -cne '{7DA9CC6C-417F-5E58-980D-3CF0591E7219}') {
    throw "unexpected deterministic contract for v0.0.7: $($first | ConvertTo-Json -Compress)"
}
$repeat = Get-Contract 'v0.0.7'
if ($repeat.product_code -cne $first.product_code -or $repeat.package_code -cne $first.package_code) { throw 'same MSI version did not reproduce deterministic MSI codes' }
if ($first.package_code -ceq $first.product_code) { throw 'MSI package code must be distinct from ProductCode' }
$next = Get-Contract 'v0.0.8'
if ($next.product_code -ceq $first.product_code -or $next.package_code -ceq $first.package_code) { throw 'different MSI versions must have different deterministic codes' }
if ($next.zip_asset -cne 'ajq_0.0.8_Windows_x86_64.zip' -or $next.msi_asset -cne 'ajq_0.0.8_Windows_x86_64.msi') {
    throw 'MSI asset contract is not case-sensitive canonical Windows x64 naming'
}
foreach ($invalidTag in @('v0.0.8-rc.1', 'v01.0.0', 'v256.0.0', 'v1.256.0', 'v1.2.65536')) {
    try {
        & $contract -Tag $invalidTag | Out-Null
        throw "MSI-inexpressible tag unexpectedly produced an identity: $invalidTag"
    } catch {
        if ($_.Exception.Message -notmatch '(stable vX\.Y\.Z|version components)') { throw }
    }
}
$boundary = Get-Contract 'v255.255.65535'
if ($boundary.version -ne '255.255.65535') { throw 'MSI version boundary did not remain valid' }

foreach ($needle in @(
    'ProductCode="$(var.ProductCode)"',
    'UpgradeCode="{BA73FC93-6FEE-410C-A647-596319F7BC1F}"',
    'Scope="perUser"',
    '<MajorUpgrade DowngradeErrorMessage=',
    'Id="AjqExecutable"',
    'Id="LicenseFile"',
    'Bitness="always64"',
    'Name="PATH"',
    'Action="set"',
    'Part="first"',
    'System="no"',
    'Value="[INSTALLFOLDER]"',
    'Id="LocalAppDataFolder"',
    'Name="ajq"'
)) {
    if (-not $wix.Contains($needle)) { throw "WiX MSI contract is missing $needle" }
}

foreach ($needle in @(
    '$summary.Property(9) = $PackageCode',
    '$summary.Property(12) = $reproducibleTime',
    '$summary.Property(13) = $reproducibleTime',
    "[datetime]'2000-01-01T00:00:00'"
)) {
    if (-not $msiFinalizer.Contains($needle)) { throw "MSI finalizer is missing reproducibility control: $needle" }
}

foreach ($needle in @(
    'name: Create draft GitHub release',
    'name: Build Windows x64 MSI',
    'name: Finalize checksums, attest, and publish',
    'if: github.event_name == ''push''',
    'dotnet tool install --global wix --version 4.0.5',
    'release ZIP must contain exactly one root ajq.exe',
    'release ZIP must contain root LICENSE',
    'windows_pe_machine.ps1 -BinaryPath $binary',
    'cannot be represented by Windows Installer ProductVersion',
    'id: release_zip',
    'BINARY_SOURCE: ${{ steps.release_zip.outputs.binary }}',
    'LICENSE_SOURCE: ${{ steps.release_zip.outputs.license }}',
    'gh release download $env:RELEASE_TAG --pattern $env:ZIP_ASSET --dir stage',
    'Trusted Signing credentials are incomplete; producing an UNSIGNED MSI.',
    'uses: azure/trusted-signing-action@208f8af4bf26cf2af8597424e3cb5582801523ba # v2.0.0',
    'refusing to replace assets on published release',
    'replacing deterministic assets on existing draft',
    'name: Publish Homebrew cask after release finalization',
    'Build-ReproducibleMsi',
    'windows_msi_finalize.ps1 -MsiPath $out -PackageCode $env:PACKAGE_CODE',
    'same verified inputs produced different unsigned MSI bytes',
    'expected_assets=(',
    'draft release must contain exactly one %s',
    'draft release assets must exactly match the expected archive/MSI allowlist',
    'mapfile -t downloaded_assets',
    'ajq_${RELEASE_VERSION}_Darwin_arm64.tar.gz',
    'ajq_${RELEASE_VERSION}_Darwin_x86_64.tar.gz',
    'ajq_${RELEASE_VERSION}_Linux_arm64.tar.gz',
    'ajq_${RELEASE_VERSION}_Linux_x86_64.tar.gz',
    'ajq_${RELEASE_VERSION}_Windows_x86_64.zip',
    'ajq_${RELEASE_VERSION}_Windows_x86_64.msi',
    'gh release edit "$RELEASE_TAG" --draft=false',
    'dist/ajq_${{ needs.validate-release.outputs.version }}_Windows_x86_64.msi'
)) {
    if (-not $workflow.Contains($needle)) { throw "release workflow is missing required MSI contract: $needle" }
}
foreach ($credential in @('AZURE_TENANT_ID', 'AZURE_CLIENT_ID', 'AZURE_CLIENT_SECRET', 'TRUSTED_SIGNING_ENDPOINT', 'TRUSTED_SIGNING_ACCOUNT', 'TRUSTED_SIGNING_PROFILE')) {
    if (-not $workflow.Contains($credential)) { throw "Trusted Signing credential gate omits $credential" }
}
if ($workflow.Contains('dist/*.tar.gz') -or $workflow.Contains('dist/*.zip') -or $workflow.Contains('dist/*.msi')) {
    throw 'provenance must attest only the explicit final asset allowlist'
}
if ($workflow.IndexOf('name: Build Windows x64 MSI') -ge $workflow.IndexOf('name: Finalize checksums, attest, and publish')) {
    throw 'MSI finalization must run after MSI upload'
}
if ($workflow.IndexOf('name: Finalize checksums, attest, and publish') -ge $workflow.IndexOf('name: Publish Homebrew cask after release finalization')) {
    throw 'Homebrew publication must wait for successful MSI finalization'
}
if ($goreleaser -notmatch '(?m)^\s*draft:\s*true\s*$') { throw 'GoReleaser must create a draft until MSI finalization succeeds' }
if ($goreleaser -notmatch '(?m)^\s*replace_existing_artifacts:\s*true\s*$') { throw 'GoReleaser must replace deterministic assets on draft retries' }
if ($workflow -notmatch "release-dry-run:\s*\r?\n\s*name: GoReleaser snapshot dry-run\s*\r?\n\s*if: github.event_name == 'pull_request'") {
    throw 'PR path must remain snapshot-only'
}

Write-Host 'Windows MSI contract tests passed'
