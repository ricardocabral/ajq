#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/package_manager_smoke.sh --channel homebrew --tag vX.Y.Z

Clean-install the requested Homebrew cask, validate its exact version, and run
the byte-stable mock-backend query without using user config or cache state.
EOF
}

channel=''
tag=''
while [[ $# -gt 0 ]]; do
  case "$1" in
    --channel) channel=${2:-}; shift 2 ;;
    --tag) tag=${2:-}; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'unknown argument: %s\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
done
[[ "$channel" == homebrew ]] || { printf 'unsupported channel: %s\n' "$channel" >&2; exit 2; }
[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { printf 'tag must be vX.Y.Z, got %s\n' "$tag" >&2; exit 2; }
version=${tag#v}
brew_bin=${BREW_BIN:-brew}
command -v "$brew_bin" >/dev/null 2>&1 || { printf 'required tool not found: brew\n' >&2; exit 127; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

cask_version() {
  "$brew_bin" info --cask --json=v2 ricardocabral/tap/ajq |
    python3 -c 'import json, sys; print(json.load(sys.stdin)["casks"][0]["version"])'
}

# Uninstall first so a pre-existing cask or its executable cannot satisfy this check.
"$brew_bin" uninstall --cask --zap ricardocabral/tap/ajq >/dev/null 2>&1 || true
"$brew_bin" update
"$brew_bin" tap ricardocabral/tap
[[ "$(cask_version)" == "$version" ]] || {
  printf 'Homebrew cask version mismatch: expected %s\n' "$version" >&2
  exit 1
}
"$brew_bin" install --cask ricardocabral/tap/ajq
"$brew_bin" list --cask ricardocabral/tap/ajq >/dev/null
[[ "$(cask_version)" == "$version" ]] || {
  printf 'installed Homebrew cask version mismatch: expected %s\n' "$version" >&2
  exit 1
}
ajq_bin=$("$brew_bin" --prefix)/bin/ajq
[[ -x "$ajq_bin" ]] || { printf 'installed Homebrew executable not found: %s\n' "$ajq_bin" >&2; exit 1; }

version_file="$tmp/version"
printf 'ajq v%s\n' "$version" >"$tmp/expected-version"
"$ajq_bin" --version >"$version_file"
cmp -s "$tmp/expected-version" "$version_file" || {
  printf 'ajq version mismatch: expected exact ajq v%s output\n' "$version" >&2
  exit 1
}
printf 'Homebrew installed version: %s\n' "$(tr -d '\n' <"$version_file")"

actual_file="$tmp/mock-output"
printf '1\n' >"$tmp/expected-mock-output"
printf '[{"id":1,"msg":"refund request"},{"id":2,"msg":"shipping update"}]\n' |
  env HOME="$tmp/home" XDG_CONFIG_HOME="$tmp/config" AJQ_CONFIG="$tmp/ajq.toml" AJQ_CACHE_DIR="$tmp/cache" \
  "$ajq_bin" --backend mock -c '.[] | select(.msg =~ "refund") | .id' >"$actual_file"
cmp -s "$tmp/expected-mock-output" "$actual_file" || {
  printf 'mock query mismatch: expected exact stdout bytes 1\\n\n' >&2
  exit 1
}
printf 'Homebrew mock stdout base64: %s\n' "$(base64 <"$actual_file" | tr -d '\n')"
printf 'Homebrew package smoke passed for %s\n' "$tag"
