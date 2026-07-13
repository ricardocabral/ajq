#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
helper="$repo_root/scripts/release_publish_preflight.sh"

expect_failure() {
  local name=$1 value=$2 output status
  set +e
  output=$(env HOMEBREW_TAP_GITHUB_TOKEN="$value" "$helper" 2>&1)
  status=$?
  set -e
  if [[ $status -eq 0 || $output != 'missing required secret: HOMEBREW_TAP_GITHUB_TOKEN' ]]; then
    printf '%s: expected credential-safe failure, got status %d and output %q\n' "$name" "$status" "$output" >&2
    return 1
  fi
}

set +e
unset_output=$(env -u HOMEBREW_TAP_GITHUB_TOKEN "$helper" 2>&1)
unset_status=$?
set -e
if [[ $unset_status -eq 0 || $unset_output != 'missing required secret: HOMEBREW_TAP_GITHUB_TOKEN' ]]; then
  printf 'unset: expected credential-safe failure, got status %d and output %q\n' "$unset_status" "$unset_output" >&2
  exit 1
fi
expect_failure empty ''
expect_failure whitespace '   '
env HOMEBREW_TAP_GITHUB_TOKEN='synthetic-token' "$helper"
printf 'release publish preflight tests passed\n'
