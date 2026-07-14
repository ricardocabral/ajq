#!/usr/bin/env bash
set -euo pipefail

# Require the tap credential only for the real release-publish job. This helper
# intentionally reports only the secret name, never its value.
if [[ -z "${HOMEBREW_TAP_GITHUB_TOKEN:-}" || "${HOMEBREW_TAP_GITHUB_TOKEN}" =~ ^[[:space:]]*$ ]]; then
  printf 'missing required secret: HOMEBREW_TAP_GITHUB_TOKEN\n' >&2
  exit 1
fi
