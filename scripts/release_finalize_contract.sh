#!/usr/bin/env bash
# Validate the release asset contract before the release workflow publishes it.
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage:
  scripts/release_finalize_contract.sh expected-assets <version>
  scripts/release_finalize_contract.sh validate-asset-manifest <asset-names-file> <version>
  scripts/release_finalize_contract.sh validate-assets <dist-dir> <version>
  scripts/release_finalize_contract.sh write-checksums <dist-dir> <version>
  scripts/release_finalize_contract.sh attestation-subjects <dist-dir> <version>
  scripts/release_finalize_contract.sh release-rerun-guard <tag>
EOF
}

expected_assets() {
  local version=$1
  printf '%s\n' \
    "ajq_${version}_Darwin_arm64.tar.gz" \
    "ajq_${version}_Darwin_x86_64.tar.gz" \
    "ajq_${version}_Linux_arm64.tar.gz" \
    "ajq_${version}_Linux_x86_64.tar.gz" \
    "ajq_${version}_Windows_x86_64.zip" \
    "ajq_${version}_Windows_x86_64.msi"
}

validate_asset_manifest() {
  local asset_names_file=$1 version=$2
  local -a expected actual
  mapfile -t expected < <({ expected_assets "$version"; printf '%s\n' checksums.txt; } | LC_ALL=C sort)
  mapfile -t actual < <(LC_ALL=C sort "$asset_names_file")
  if ! diff -u <(printf '%s\n' "${expected[@]}") <(printf '%s\n' "${actual[@]}"); then
    printf 'draft release assets must exactly match the expected archive/MSI/checksum allowlist\n' >&2
    return 1
  fi
}

validate_assets() {
  local dist_dir=$1 version=$2
  local -a expected actual
  mapfile -t expected < <(expected_assets "$version" | LC_ALL=C sort)
  mapfile -t actual < <(find "$dist_dir" -maxdepth 1 -type f \( -name 'ajq_*.tar.gz' -o -name 'ajq_*.zip' -o -name 'ajq_*.msi' \) -exec basename {} \; | LC_ALL=C sort)
  if ! diff -u <(printf '%s\n' "${expected[@]}") <(printf '%s\n' "${actual[@]}"); then
    printf 'draft release assets must exactly match the expected archive/MSI allowlist\n' >&2
    return 1
  fi

  mapfile -t expected < <(expected_assets "$version" | LC_ALL=C sort)
  local asset count
  for asset in "${expected[@]}"; do
    count=$(find "$dist_dir" -maxdepth 1 -type f -name "$asset" -exec printf x \; | wc -c | tr -d ' ')
    if [ "$count" != 1 ]; then
      printf 'draft release must contain exactly one %s\n' "$asset" >&2
      return 1
    fi
  done
}

write_checksums() {
  local dist_dir=$1 version=$2
  local -a expected
  validate_assets "$dist_dir" "$version"
  mapfile -t expected < <(expected_assets "$version" | LC_ALL=C sort)
  (
    cd "$dist_dir"
    sha256sum "${expected[@]}" > checksums.txt
    sha256sum --check --strict checksums.txt
  )
}

validate_checksum_manifest() {
  local dist_dir=$1 version=$2
  local manifest="$dist_dir/checksums.txt"
  local -a expected actual
  [ -f "$manifest" ] || {
    printf 'checksums.txt must exist before provenance subjects are generated\n' >&2
    return 1
  }
  if ! awk 'NF != 2 || $1 !~ /^[[:xdigit:]]{64}$/ { exit 1 } { print $2 }' "$manifest" >"$manifest.asset-names"; then
    rm -f "$manifest.asset-names"
    printf 'checksums.txt must contain only SHA-256 filename entries\n' >&2
    return 1
  fi
  mapfile -t expected < <(expected_assets "$version" | LC_ALL=C sort)
  mapfile -t actual < <(LC_ALL=C sort "$manifest.asset-names")
  rm -f "$manifest.asset-names"
  if ! diff -u <(printf '%s\n' "${expected[@]}") <(printf '%s\n' "${actual[@]}"); then
    printf 'checksums.txt must contain exactly one entry for every expected archive/MSI\n' >&2
    return 1
  fi
}

attestation_subjects() {
  local dist_dir=$1 version=$2 asset
  validate_assets "$dist_dir" "$version"
  validate_checksum_manifest "$dist_dir" "$version"
  (
    cd "$dist_dir"
    sha256sum --check --strict checksums.txt >/dev/null
  )
  while IFS= read -r asset; do
    printf '%s/%s\n' "$dist_dir" "$asset"
  done < <(expected_assets "$version")
  printf '%s/checksums.txt\n' "$dist_dir"
}

release_rerun_guard() {
  local tag=$1 response status
  local stderr_file
  stderr_file=$(mktemp)
  if response=$(gh api "repos/${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}/releases/tags/$tag" --jq .draft 2>"$stderr_file"); then
    rm -f "$stderr_file"
    case "$(printf '%s' "$response" | tr -d '[:space:]')" in
      true)
        printf 'replacing deterministic assets on existing draft %s\n' "$tag"
        ;;
      false)
        printf 'refusing to replace assets on published release %s\n' "$tag" >&2
        return 1
        ;;
      *)
        printf 'release lookup returned malformed draft status for %s\n' "$tag" >&2
        return 1
        ;;
    esac
  else
    status=$?
    if [ "$status" -eq 1 ] && grep -Fxq 'gh: Not Found (HTTP 404)' "$stderr_file"; then
      rm -f "$stderr_file"
      printf 'no existing release for %s; draft creation may proceed\n' "$tag"
      return 0
    fi
    cat "$stderr_file" >&2
    rm -f "$stderr_file"
    printf 'unable to determine release state for %s; refusing to continue\n' "$tag" >&2
    return 1
  fi
}

[ "$#" -ge 1 ] || { usage; exit 2; }
case "$1" in
  expected-assets)
    [ "$#" -eq 2 ] || { usage; exit 2; }
    expected_assets "$2"
    ;;
  validate-asset-manifest)
    [ "$#" -eq 3 ] || { usage; exit 2; }
    validate_asset_manifest "$2" "$3"
    ;;
  validate-assets)
    [ "$#" -eq 3 ] || { usage; exit 2; }
    validate_assets "$2" "$3"
    ;;
  write-checksums)
    [ "$#" -eq 3 ] || { usage; exit 2; }
    write_checksums "$2" "$3"
    ;;
  attestation-subjects)
    [ "$#" -eq 3 ] || { usage; exit 2; }
    attestation_subjects "$2" "$3"
    ;;
  release-rerun-guard)
    [ "$#" -eq 2 ] || { usage; exit 2; }
    release_rerun_guard "$2"
    ;;
  *)
    usage
    exit 2
    ;;
esac
