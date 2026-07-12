#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

usage() {
  cat <<'EOF'
Usage: scripts/release_smoke.sh

Run the full release smoke suite. The suite uses a source-built CLI for
hermetic discovery checks, then runs test, lint, packaging, installer, and
website gates.

Options:
  -h, --help  Show this help and exit.
EOF
}

case "${1:-}" in
  "") ;;
  -h | --help)
    usage
    exit 0
    ;;
  *)
    printf 'unknown option: %s\n\n' "$1" >&2
    usage >&2
    exit 2
    ;;
esac

step() {
  printf '\n==> %s\n' "$1"
}

run() {
  step "$1"
  shift
  "$@"
}

require_env() {
  local name
  for name in "$@"; do
    if [ -z "${!name:-}" ]; then
      printf 'missing required environment variable: %s\n' "$name" >&2
      return 1
    fi
  done
}

run_mock_smoke() {
  local cache_dir out
  cache_dir=$(mktemp -d)
  step "mock semantic smoke"
  out=$(
    printf '[{"id":1,"msg":"refund request"},{"id":2,"msg":"shipping update"}]\n' |
      env AJQ_CACHE_DIR="$cache_dir" AJQ_CONFIG= go run ./cmd/ajq --backend mock -c \
        '.[] | select(.msg =~ "refund") | .id'
  )
  if [ "$out" != "1" ]; then
    printf 'mock smoke returned %q, want 1\n' "$out" >&2
    return 1
  fi
}

run_local_provision_check() {
  local cache_dir out status
  cache_dir=$(mktemp -d)
  step "local provisioning dry check"
  set +e
  out=$(env AJQ_CACHE_DIR="$cache_dir" AJQ_CONFIG= go run ./cmd/ajq provision --check 2>&1)
  status=$?
  set -e

  if [ "$status" -ne 0 ] && [ "$status" -ne 1 ]; then
    printf '%s\n' "$out" >&2
    printf 'ajq provision --check exited %d, want 0 or missing-assets status 1\n' "$status" >&2
    return 1
  fi
  if [[ "$out" != *"platform:"* ]] ||
    { [[ "$out" != *"all assets present: nothing to provision"* ]] &&
      [[ "$out" != *"provisioning required: run \`ajq provision\` to install missing assets"* ]]; }; then
    printf '%s\n' "$out" >&2
    printf 'ajq provision --check did not report the expected provisioning status\n' >&2
    return 1
  fi
}

run_discovery_smoke() (
  local audit_dir gocache gomodcache gopath provision_status
  audit_dir=$(mktemp -d)
  trap 'rm -rf "$audit_dir"' EXIT
  gocache=$(go env GOCACHE)
  gomodcache=$(go env GOMODCACHE)
  gopath=$(go env GOPATH)

  run_ajq() {
    env -i \
      PATH="$PATH" \
      HOME="$audit_dir/home" \
      XDG_CONFIG_HOME="$audit_dir/config" \
      AJQ_CONFIG= \
      AJQ_CACHE_DIR="$audit_dir/cache" \
      "$audit_dir/ajq" "$@"
  }

  build_source_ajq() {
    env -i \
      PATH="$PATH" \
      HOME="$audit_dir/home" \
      XDG_CONFIG_HOME="$audit_dir/config" \
      AJQ_CONFIG= \
      AJQ_CACHE_DIR="$audit_dir/cache" \
      GOCACHE="$gocache" \
      GOMODCACHE="$gomodcache" \
      GOPATH="$gopath" \
      go build -o "$audit_dir/ajq" ./cmd/ajq
  }

  assert_no_stderr() {
    if [ -s "$1" ]; then
      printf 'unexpected stderr from discovery command:\n' >&2
      cat "$1" >&2
      return 1
    fi
  }

  assert_json_v1() {
    python3 - "$1" <<'PY'
import json
import pathlib
import sys

raw = pathlib.Path(sys.argv[1]).read_text()
if not raw.endswith("\n"):
    raise SystemExit("JSON probe did not end with a newline")
value = json.loads(raw)
if not isinstance(value, dict) or value.get("schema_version") != "1":
    raise SystemExit("JSON probe did not return a v1 object")
PY
  }

  mkdir -p "$audit_dir/home" "$audit_dir/config" "$audit_dir/cache"
  step "hermetic discovery CLI smoke"
  build_source_ajq

  run_ajq examples >"$audit_dir/examples.out" 2>"$audit_dir/examples.err"
  assert_no_stderr "$audit_dir/examples.err"
  grep -Fq 'Semantic examples use --backend mock and require no model, network access, or API key.' "$audit_dir/examples.out"

  run_ajq capabilities --json >"$audit_dir/capabilities.json" 2>"$audit_dir/capabilities.err"
  assert_no_stderr "$audit_dir/capabilities.err"
  assert_json_v1 "$audit_dir/capabilities.json"

  run_ajq models list --json >"$audit_dir/models.json" 2>"$audit_dir/models.err"
  assert_no_stderr "$audit_dir/models.err"
  assert_json_v1 "$audit_dir/models.json"

  run_ajq cache status --json >"$audit_dir/cache.json" 2>"$audit_dir/cache.err"
  assert_no_stderr "$audit_dir/cache.err"
  assert_json_v1 "$audit_dir/cache.json"

  set +e
  run_ajq provision --check --json >"$audit_dir/provision.json" 2>"$audit_dir/provision.err"
  provision_status=$?
  set -e
  if [ "$provision_status" -ne 0 ] && [ "$provision_status" -ne 1 ]; then
    printf 'ajq provision --check --json exited %d, want 0 or missing-assets status 1\n' "$provision_status" >&2
    return 1
  fi
  assert_no_stderr "$audit_dir/provision.err"
  assert_json_v1 "$audit_dir/provision.json"
)

run_openrouter_live_smoke() {
  local cache_dir out
  require_env OPENROUTER_API_KEY AJQ_OPENROUTER_MODEL
  cache_dir=$(mktemp -d)
  step "OpenRouter live CLI smoke"
  out=$(
    printf '{"msg":"urgent outage"}\n' |
      env AJQ_CACHE_DIR="$cache_dir" AJQ_CONFIG= go run ./cmd/ajq \
        --backend openrouter --model "$AJQ_OPENROUTER_MODEL" --max-calls 1 -c \
        '.msg =~ "urgent"'
  )
  case "$out" in
    true | false) ;;
    *)
      printf 'OpenRouter smoke returned %q, want boolean output\n' "$out" >&2
      return 1
      ;;
  esac
}

run_local_live_smoke() {
  require_env AJQ_LOCAL_BASE_URL AJQ_LOCAL_MODEL
  step "local backend live conformance"
  AJQ_CONFORMANCE_LIVE=1 go test -v ./internal/backend/local -run TestLocalBackendLiveConformance
}

run "standard tests" make test
run "lint" make lint
run "shell scripts lint" shellcheck scripts/install.sh scripts/install_test.sh scripts/release_smoke.sh \
  scripts/release_publish_preflight.sh scripts/release_publish_preflight_test.sh \
  scripts/package_manager_smoke.sh scripts/package_manager_smoke_test.sh
run "release publication preflight tests" scripts/release_publish_preflight_test.sh
run "package-manager smoke tests" scripts/package_manager_smoke_test.sh
run_discovery_smoke
run_mock_smoke
run_local_provision_check
run "release snapshot" make release-snapshot
run "installer snapshot smoke" scripts/install_test.sh
run "website build" env WEBSITE_INSTALL_CMD="npm ci --offline" make website-build

if [ "${AJQ_PROVISION_LIVE:-}" = "1" ]; then
  run "live provisioning engine bundle smoke" go test -v ./internal/provision -run TestInstallEngineBundleLiveDownloadOptIn
fi

if [ "${AJQ_MODELS_LIVE:-}" = "1" ]; then
  run "live model pull smoke" go test -v ./internal/cli -run TestModelsLivePullOptIn
fi

if [ "${AJQ_OLLAMA_LIVE:-}" = "1" ]; then
  run "Ollama live CLI smoke" go test -v ./internal/cli -run TestOllamaLiveSmokeOptIn
fi

if [ "${AJQ_ANTHROPIC_LIVE:-}" = "1" ]; then
  run "Anthropic live CLI smoke" go test -v ./internal/cli -run TestAnthropicLiveSmokeOptIn
fi

if [ "${AJQ_LOCAL_LIVE:-}" = "1" ]; then
  run_local_live_smoke
fi

if [ "${AJQ_CONFORMANCE_LIVE:-}" = "1" ]; then
  run "live backend conformance" go test -v \
    ./internal/backend/local \
    ./internal/backend/ollamabk \
    ./internal/backend/oai \
    ./internal/backend/anthropicbk \
    -run LiveConformance
fi

if [ "${AJQ_OPENROUTER_LIVE:-}" = "1" ]; then
  run_openrouter_live_smoke
fi

printf '\nrelease smoke complete\n'
