#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT

cat >"$test_dir/golangci-lint" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [ "$*" = 'version --short' ]; then
  printf '%s\n' "$AJQ_TEST_LINT_VERSION"
else
  printf '%s\n' "$*" >"$AJQ_TEST_LINT_CALLS"
  exit "${AJQ_TEST_LINT_EXIT:-0}"
fi
EOF
chmod +x "$test_dir/golangci-lint"
export AJQ_TEST_LINT_CALLS="$test_dir/calls"

for lint_version in 2.13.2 2.8.0; do
  if AJQ_TEST_LINT_VERSION="$lint_version" make -s -C "$repo_root" lint \
    GOLANGCI_LINT="$test_dir/golangci-lint" >"$test_dir/output" 2>&1; then
    printf 'make lint must reject golangci-lint %s\n' "$lint_version" >&2
    exit 1
  fi
  test ! -f "$AJQ_TEST_LINT_CALLS"
  grep -Fq 'requires golangci-lint v2.9.0' "$test_dir/output"
done

AJQ_TEST_LINT_VERSION=2.9.0 make -s -C "$repo_root" lint \
  GOLANGCI_LINT="$test_dir/golangci-lint"
test "$(cat "$AJQ_TEST_LINT_CALLS")" = 'run ./...'

if AJQ_TEST_LINT_VERSION=2.9.0 AJQ_TEST_LINT_EXIT=7 make -s -C "$repo_root" lint \
  GOLANGCI_LINT="$test_dir/golangci-lint" >"$test_dir/output" 2>&1; then
  printf 'make lint must propagate linter failures\n' >&2
  exit 1
fi

printf 'Lint version policy tests passed\n'
