---
title: "Configure defaults"
linkTitle: "Configure defaults"
weight: 8
description: >
  Set backend, model, base URL, cost cap, and cache defaults in config.toml.
---

Use a config file when you do not want to repeat the same semantic-backend flags on every
command.

## 1. Create the config file

ajq reads this path by default:

```text
${XDG_CONFIG_HOME:-~/.config}/ajq/config.toml
```

Create the directory and file:

```bash
mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/ajq"
cat > "${XDG_CONFIG_HOME:-$HOME/.config}/ajq/config.toml" <<'EOF'
backend = "mock"
max_calls = 25
no_cache = false
EOF
```

For a one-off location, set `AJQ_CONFIG` to an explicit file path.

## 2. Add semantic defaults

Recognized keys are:

```toml
backend = "local"              # mock, local, ollama, openai, openrouter, anthropic
model = "qwen2.5-1.5b"
base_url = "http://127.0.0.1:8081"
max_calls = 100                 # 0 = unlimited
no_cache = false
```

For example, to default to the deterministic mock backend with a small call cap:

```toml
backend = "mock"
max_calls = 1
no_cache = true
```

Then a semantic query can omit `--backend`:

```bash
printf '[{"msg":"urgent"},{"msg":"other"}]' \
  | ajq '.[] | select(.msg =~ "urgent") | .msg'
```

With `max_calls = 1`, that example stops before making the second distinct judgement.

## 3. Override a default when needed

Precedence is:

1. Command-line flags
2. `AJQ_*` environment variables
3. `config.toml`
4. Backend defaults

A flag wins for one command:

```bash
printf '[{"msg":"urgent"},{"msg":"other"}]' \
  | ajq --max-calls 0 '.[] | select(.msg =~ "urgent") | .msg'
```

Environment variables are useful for a shell session or CI job:

```bash
export AJQ_BACKEND=mock
export AJQ_MAX_CALLS=0
```

The config file still supplies any setting that the flag or environment did not override.

## 4. Keep API keys out of config

API keys are environment-only:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
export OPENAI_API_KEY="sk-..."
export OPENROUTER_API_KEY="sk-or-..."
```

If a config file contains credential-looking keys such as `api_key`, `apikey`, or `token`,
ajq rejects it:

```text
ajq: error: config key "api_key" looks like a credential; API keys are env-only
```

## Related

- [Configuration reference](../../reference/configuration/) — complete key and env tables.
- [Backends reference](../../reference/backends/) — backend-specific defaults.
