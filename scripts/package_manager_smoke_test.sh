#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
smoke="$repo_root/scripts/package_manager_smoke.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/prefix/bin"

cat >"$tmp/prefix/bin/ajq" <<'EOF'
#!/usr/bin/env bash
if [[ "$1" == --version ]]; then
  printf '%b' "${AJQ_VERSION_OUTPUT:-ajq v1.2.3\n}"
else
  cat >/dev/null
  printf '%b' "${QUERY_OUTPUT:-1\n}"
fi
EOF
chmod +x "$tmp/prefix/bin/ajq"

cat >"$tmp/brew" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$BREW_LOG"
case "$1" in
  uninstall|update|tap|install|list) exit 0 ;;
  info) printf '{"casks":[{"version":"%s"}]}' "${CASK_VERSION:-1.2.3}" ;;
  --prefix) printf '%s\n' "$BREW_PREFIX" ;;
  *) printf 'unexpected brew command: %s\n' "$*" >&2; exit 2 ;;
esac
EOF
chmod +x "$tmp/brew"

run_smoke() {
  env BREW_BIN="$tmp/brew" BREW_LOG="$tmp/brew.log" BREW_PREFIX="$tmp/prefix" "$@" \
    "$smoke" --channel homebrew --tag v1.2.3
}

expect_failure() {
  local name=$1 expected=$2 output status
  shift 2
  set +e
  output=$(run_smoke "$@" 2>&1)
  status=$?
  set -e
  if [[ $status -eq 0 || $output != *"$expected"* ]]; then
    printf '%s: expected failure containing %q, got status %d and output %q\n' "$name" "$expected" "$status" "$output" >&2
    return 1
  fi
}

: >"$tmp/brew.log"
success_output=$(run_smoke env AJQ_VERSION_OUTPUT='ajq v1.2.3\n' QUERY_OUTPUT='1\n')
[[ "$success_output" == *'Homebrew installed version: ajq v1.2.3'* ]] || { printf 'success evidence omitted exact version\n' >&2; exit 1; }
[[ "$success_output" == *'Homebrew mock stdout base64: MQo='* ]] || { printf 'success evidence omitted mock stdout bytes\n' >&2; exit 1; }
for command in \
  'uninstall --cask --zap ricardocabral/tap/ajq' \
  'update' \
  'tap ricardocabral/tap' \
  'install --cask ricardocabral/tap/ajq'; do
  grep -Fxq "$command" "$tmp/brew.log" || { printf 'missing brew construction: %s\n' "$command" >&2; exit 1; }
done

expect_failure cask-version-mismatch 'Homebrew cask version mismatch' env CASK_VERSION=9.9.9
expect_failure executable-version-prefix 'ajq version mismatch' env AJQ_VERSION_OUTPUT='prefix ajq v1.2.3\n'
expect_failure executable-version-suffix 'ajq version mismatch' env AJQ_VERSION_OUTPUT='ajq v1.2.3 suffix\n'
expect_failure query-mismatch 'mock query mismatch' env QUERY_OUTPUT='2\n'
expect_failure query-crlf 'mock query mismatch' env QUERY_OUTPUT='1\r\n'
expect_failure query-extra-byte 'mock query mismatch' env QUERY_OUTPUT='1\n2\n'
set +e
missing_output=$(env BREW_BIN="$tmp/no-brew" "$smoke" --channel homebrew --tag v1.2.3 2>&1)
missing_status=$?
set -e
if [[ $missing_status -eq 0 || $missing_output != *'required tool not found: brew'* ]]; then
  printf 'missing-tool: unexpected result %d %q\n' "$missing_status" "$missing_output" >&2
  exit 1
fi
printf 'package-manager smoke shell tests passed\n'
