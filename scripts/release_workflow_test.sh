#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
workflow="$repo_root/.github/workflows/release.yml"
winget_workflow="$repo_root/.github/workflows/winget.yml"
goreleaser_config="$repo_root/.goreleaser.yaml"
finalize_contract="$repo_root/scripts/release_finalize_contract.sh"

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
grep -Fq 'scripts/release_finalize_contract.sh validate-asset-manifest' "$workflow" || {
  printf 'Release workflow must validate the remote asset manifest before download\n' >&2
  exit 1
}
grep -Fq 'scripts/release_finalize_contract.sh write-checksums' "$workflow" || {
  printf 'Release workflow must generate checksums through the finalizer contract\n' >&2
  exit 1
}
grep -Fq 'scripts/release_finalize_contract.sh attestation-subjects' "$workflow" || {
  printf 'Release workflow must derive provenance subjects through the finalizer contract\n' >&2
  exit 1
}
# shellcheck disable=SC2016 # The literal workflow expression must not expand in this test.
grep -Fq 'subject-path: ${{ steps.provenance_subjects.outputs.subjects }}' "$workflow" || {
  printf 'Release workflow provenance must consume the generated explicit subject set\n' >&2
  exit 1
}
grep -Fq 'steps.release_zip.outputs.binary' "$workflow" || {
  printf 'Release workflow must build MSI from the verified release ZIP binary\n' >&2
  exit 1
}
# shellcheck disable=SC2016 # The literal workflow fragment intentionally includes $binary.
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
grep -Fq 'scripts/release_finalize_contract.sh release-rerun-guard' "$workflow" || {
  printf 'Release workflow must guard release reruns through the finalizer contract\n' >&2
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
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT
version=9.8.7
asset_manifest="$test_dir/assets.txt"
dist="$test_dir/dist"
mkdir -p "$dist"
mapfile -t assets < <("$finalize_contract" expected-assets "$version")
{ printf '%s\n' "${assets[@]}"; printf '%s\n' checksums.txt; } >"$asset_manifest"
for asset in "${assets[@]}"; do
  printf 'fixture for %s\n' "$asset" >"$dist/$asset"
done

"$finalize_contract" validate-asset-manifest "$asset_manifest" "$version"
"$finalize_contract" validate-assets "$dist" "$version"
"$finalize_contract" write-checksums "$dist" "$version"
(
  cd "$dist"
  sha256sum --check --strict checksums.txt
)
mapfile -t subjects < <("$finalize_contract" attestation-subjects "$dist" "$version")
expected_subjects=()
for asset in "${assets[@]}"; do
  expected_subjects+=("$dist/$asset")
done
expected_subjects+=("$dist/checksums.txt")
diff -u <(printf '%s\n' "${expected_subjects[@]}") <(printf '%s\n' "${subjects[@]}")

expect_failure() {
  if "$@" >/dev/null 2>&1; then
    printf 'expected failure: %q\n' "$*" >&2
    exit 1
  fi
}
missing_manifest="$test_dir/missing.txt"
{ printf '%s\n' "${assets[@]:1}"; printf '%s\n' checksums.txt; } >"$missing_manifest"
expect_failure "$finalize_contract" validate-asset-manifest "$missing_manifest" "$version"
duplicate_manifest="$test_dir/duplicate.txt"
{ printf '%s\n' "${assets[@]}"; printf '%s\n' checksums.txt "${assets[0]}"; } >"$duplicate_manifest"
expect_failure "$finalize_contract" validate-asset-manifest "$duplicate_manifest" "$version"
unexpected_manifest="$test_dir/unexpected.txt"
{ printf '%s\n' "${assets[@]}"; printf '%s\n' checksums.txt "ajq_${version}_Linux_ppc64le.tar.gz"; } >"$unexpected_manifest"
expect_failure "$finalize_contract" validate-asset-manifest "$unexpected_manifest" "$version"
{ printf '%s\n' "${assets[@]}"; printf '%s\n' checksums.txt 'unexpected-debug-artifact.txt'; } >"$unexpected_manifest"
expect_failure "$finalize_contract" validate-asset-manifest "$unexpected_manifest" "$version"
cp "$dist/checksums.txt" "$test_dir/complete-checksums.txt"
head -n 1 "$dist/checksums.txt" >"$test_dir/incomplete-checksums.txt"
cp "$test_dir/incomplete-checksums.txt" "$dist/checksums.txt"
expect_failure "$finalize_contract" attestation-subjects "$dist" "$version"
{ cat "$test_dir/complete-checksums.txt"; head -n 1 "$test_dir/complete-checksums.txt"; } >"$dist/checksums.txt"
expect_failure "$finalize_contract" attestation-subjects "$dist" "$version"
cp "$test_dir/complete-checksums.txt" "$dist/checksums.txt"
printf 'tampered\n' >>"$dist/${assets[0]}"
expect_failure "$finalize_contract" attestation-subjects "$dist" "$version"
"$finalize_contract" write-checksums "$dist" "$version"

fake_bin="$test_dir/bin"
mkdir -p "$fake_bin"
cat >"$fake_bin/gh" <<'EOF'
#!/usr/bin/env bash
case "${AJQ_GH_FIXTURE:-}" in
  absent) printf 'gh: Not Found (HTTP 404)\n' >&2; exit 1 ;;
  draft) printf 'true\n' ;;
  published) printf 'false\n' ;;
  malformed) printf 'not-a-bool\n' ;;
  auth) printf 'gh: Bad credentials (HTTP 401)\n' >&2; exit 1 ;;
  transport) printf 'network unavailable\n' >&2; exit 1 ;;
  *) exit 99 ;;
esac
EOF
chmod +x "$fake_bin/gh"
for fixture in absent draft; do
  PATH="$fake_bin:$PATH" AJQ_GH_FIXTURE="$fixture" GITHUB_REPOSITORY=owner/repo \
    "$finalize_contract" release-rerun-guard v9.8.7 >/dev/null
done
for fixture in published malformed auth transport; do
  expect_failure env PATH="$fake_bin:$PATH" AJQ_GH_FIXTURE="$fixture" GITHUB_REPOSITORY=owner/repo \
    "$finalize_contract" release-rerun-guard v9.8.7
done

printf 'release workflow dispatch guard tests passed\n'
