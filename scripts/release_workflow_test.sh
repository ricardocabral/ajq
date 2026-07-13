#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
workflow="$repo_root/.github/workflows/release.yml"

if grep -Eq '^[[:space:]]+workflow_dispatch:' "$workflow"; then
  printf 'Release workflow must not permit manual dispatch publication\n' >&2
  exit 1
fi
grep -Fq "if: github.event_name == 'push'" "$workflow" || {
  printf 'Release publish job must run only for tag pushes\n' >&2
  exit 1
}
grep -Fq 'args: release --clean' "$workflow" || {
  printf 'Release workflow must retain clean GoReleaser publication\n' >&2
  exit 1
}
if grep -Fq 'release.mode: replace' "$workflow"; then
  printf 'Release workflow must not replace an existing release\n' >&2
  exit 1
fi
printf 'release workflow dispatch guard tests passed\n'
