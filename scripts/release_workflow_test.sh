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
