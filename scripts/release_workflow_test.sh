#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
workflow="$repo_root/.github/workflows/release.yml"
goreleaser_config="$repo_root/.goreleaser.yaml"

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
grep -Eq '^[[:space:]]*mode:[[:space:]]*replace[[:space:]]*$' "$goreleaser_config" || {
  printf 'GoReleaser must retain release mode: replace for the immutable tag release contract\n' >&2
  exit 1
}
printf 'release workflow dispatch guard tests passed\n'
