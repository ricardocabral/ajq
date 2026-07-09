#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

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
run "shell scripts lint" shellcheck scripts/install.sh scripts/install_test.sh scripts/release_smoke.sh
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
