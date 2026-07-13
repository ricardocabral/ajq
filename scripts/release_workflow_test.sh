#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
workflow="$repo_root/.github/workflows/release.yml"
winget_workflow="$repo_root/.github/workflows/winget.yml"
goreleaser_config="$repo_root/.goreleaser.yaml"

if grep -Eq '^[[:space:]]+workflow_dispatch:' "$workflow"; then
  printf 'Release workflow must not permit manual dispatch publication\n' >&2
  exit 1
fi
grep -Fq "if: github.event_name == 'push'" "$workflow" || {
  printf 'Release publish jobs must run only for tag pushes\n' >&2
  exit 1
}
grep -Fq 'name: Create draft GitHub release' "$workflow" || {
  printf 'Release workflow must create a draft before Windows MSI packaging\n' >&2
  exit 1
}
grep -Fq 'name: Build Windows x64 MSI' "$workflow" || {
  printf 'Release workflow must include the Windows MSI job\n' >&2
  exit 1
}
grep -Fq 'name: Finalize checksums, attest, and publish' "$workflow" || {
  printf 'Release workflow must finalize only after MSI packaging\n' >&2
  exit 1
}
grep -Fq 'dotnet tool install --global wix --version 4.0.5' "$workflow" || {
  printf 'Release workflow must pin WiX 4.0.5\n' >&2
  exit 1
}
grep -Fq 'same verified inputs produced different unsigned MSI bytes' "$workflow" || {
  printf 'Release workflow must reject non-reproducible MSI retries\n' >&2
  exit 1
}
grep -Fq 'draft release must contain exactly one %s' "$workflow" || {
  printf 'Release workflow must require exact archive assets before publication\n' >&2
  exit 1
}
grep -Fq 'draft release assets must exactly match the expected archive/MSI allowlist' "$workflow" || {
  printf 'Release workflow must reject unexpected archive assets before publication\n' >&2
  exit 1
}
grep -Fq 'steps.release_zip.outputs.binary' "$workflow" || {
  printf 'Release workflow must build MSI from the verified release ZIP binary\n' >&2
  exit 1
}
grep -Fq 'windows_pe_machine.ps1 -BinaryPath $binary' "$workflow" || {
  printf 'Release workflow must validate the selected release executable is AMD64\n' >&2
  exit 1
}
grep -Fq 'cannot be represented by Windows Installer ProductVersion' "$workflow" || {
  printf 'Release workflow must reject MSI-inexpressible versions before draft creation\n' >&2
  exit 1
}
grep -Fq 'Trusted Signing credentials are incomplete; producing an UNSIGNED MSI.' "$workflow" || {
  printf 'Release workflow must retain the credential-safe unsigned MSI warning\n' >&2
  exit 1
}
grep -Fq 'azure/trusted-signing-action@208f8af4bf26cf2af8597424e3cb5582801523ba # v2.0.0' "$workflow" || {
  printf 'Release workflow must SHA-pin Azure Trusted Signing\n' >&2
  exit 1
}
grep -Fq 'refusing to replace assets on published release' "$workflow" || {
  printf 'Release workflow must reject published release reruns\n' >&2
  exit 1
}
grep -Fq 'name: Publish Homebrew cask after release finalization' "$workflow" || {
  printf 'Release workflow must defer Homebrew upload until MSI finalization\n' >&2
  exit 1
}
grep -Fq 'args: release --clean' "$workflow" || {
  printf 'Release workflow must retain clean GoReleaser publication\n' >&2
  exit 1
}
grep -Eq '^[[:space:]]*mode:[[:space:]]*replace[[:space:]]*$' "$goreleaser_config" || {
  printf 'GoReleaser must retain release mode: replace for the immutable tag release contract\n' >&2
  exit 1
}
grep -Eq '^[[:space:]]*draft:[[:space:]]*true[[:space:]]*$' "$goreleaser_config" || {
  printf 'GoReleaser must retain a draft until MSI finalization succeeds\n' >&2
  exit 1
}
grep -Eq '^[[:space:]]*replace_existing_artifacts:[[:space:]]*true[[:space:]]*$' "$goreleaser_config" || {
  printf 'GoReleaser must replace assets only for deterministic draft retries\n' >&2
  exit 1
}
grep -Fq 'The pinned releaser requires an existing winget-pkgs version as its update base.' "$winget_workflow" || {
  printf 'WinGet workflow must retain its initial-submission precondition\n' >&2
  exit 1
}
grep -Fq 'Initial registration is intentionally manual:' "$winget_workflow" || {
  printf 'WinGet workflow must retain the manual initial-submission path\n' >&2
  exit 1
}
grep -Fq 'identifier: RicardoCabral.ajq' "$winget_workflow" || {
  printf 'WinGet workflow must retain the canonical existing-upstream identifier casing\n' >&2
  exit 1
}
if grep -Fq 'Ricardocabral.ajq' "$winget_workflow"; then
  printf 'WinGet workflow must not reintroduce the rejected identifier casing\n' >&2
  exit 1
fi
printf 'release workflow dispatch guard tests passed\n'
