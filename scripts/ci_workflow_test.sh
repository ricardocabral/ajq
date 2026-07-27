#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
workflow="$repo_root/.github/workflows/ci.yml"

grep -Fq 'run_platform_validation:' "$workflow" || {
  printf 'CI must expose an opt-in platform-validation input\n' >&2
  exit 1
}
grep -Fq 'default: false' "$workflow" || {
  printf 'Platform validation must default to off\n' >&2
  exit 1
}

platform_job=$(sed -n '/^  platform-validation:/,/^  security:/p' "$workflow")
printf '%s\n' "$platform_job" | grep -Fq 'if: inputs.run_platform_validation == true' || {
  printf 'Cross-platform validation must be explicitly opted in\n' >&2
  exit 1
}
printf '%s\n' "$platform_job" | grep -Fq -- '- macos-latest' || {
  printf 'Opt-in validation must retain macOS coverage\n' >&2
  exit 1
}
printf '%s\n' "$platform_job" | grep -Fq -- '- windows-latest' || {
  printf 'Opt-in validation must retain Windows coverage\n' >&2
  exit 1
}

printf 'CI workflow cost-control tests passed\n'
