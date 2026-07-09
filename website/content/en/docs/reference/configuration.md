---
title: "Configuration"
linkTitle: "Configuration"
weight: 5
description: >
  Config files, environment variables, precedence, and credential policy.
---

ajq resolves semantic-backend settings from flags, environment variables, a TOML config
file, and backend defaults.

## Precedence

| Rank | Source | Notes |
|---:|---|---|
| 1 | Command-line flags | `--backend`, `--model`, `--base-url`, `--max-calls`, and `--no-cache`. |
| 2 | Environment variables | `AJQ_BACKEND`, `AJQ_MODEL`, `AJQ_BASE_URL`, `AJQ_MAX_CALLS`. |
| 3 | TOML config | Default path or `AJQ_CONFIG`. |
| 4 | Backend defaults | For example, Anthropic's default model and paid-backend call cap. |

Flags override environment variables; environment variables override config values; config
values override backend defaults.

## Config file path

| Setting | Behavior |
|---|---|
| `AJQ_CONFIG=/path/to/config.toml` | Read this exact file. If it is set and the file is missing or invalid, ajq errors. |
| `${XDG_CONFIG_HOME}/ajq/config.toml` | Default path when `XDG_CONFIG_HOME` is set and `AJQ_CONFIG` is not. |
| `~/.config/ajq/config.toml` | Default fallback path when neither `AJQ_CONFIG` nor `XDG_CONFIG_HOME` is set. |

A missing default config file is ignored. A missing explicit `AJQ_CONFIG` file is an error.

## TOML keys

| Key | Type | Example | Meaning |
|---|---|---|---|
| `backend` | string | `"local"` | Semantic backend: `mock`, `local`, `ollama`, `openai`, `openrouter`, or `anthropic`. |
| `model` | string | `"qwen2.5-3b"` | Model id or shipped alias for the selected backend. |
| `base_url` | string | `"http://127.0.0.1:11434"` | HTTP base URL for backends that accept one. |
| `max_calls` | integer | `100` | Maximum post-dedup backend judgements; `0` means unlimited. Must be non-negative. |
| `no_cache` | boolean | `true` | Disable persistent judgement cache reads/writes when true. |

Example:

```toml
backend = "local"
model = "qwen2.5-3b"
max_calls = 50
no_cache = false
```

`ajq models use <name>` writes `model = "<name>"` after verifying the model is installed.
It preserves unrelated keys but not comments or original formatting.

## Environment variables

| Variable | Type | Meaning |
|---|---|---|
| `AJQ_BACKEND` | string | Same values as `backend`. |
| `AJQ_MODEL` | string | Same meaning as `model`. |
| `AJQ_BASE_URL` | string | Same meaning as `base_url`. |
| `AJQ_MAX_CALLS` | non-negative integer | Same meaning as `max_calls`. |
| `AJQ_CONFIG` | path | Explicit config file path. |
| `AJQ_CACHE_DIR` | path | Cache root for provisioning assets, daemon state, local models, and judgement cache files. |
| `OLLAMA_HOST` | URL or host[:port] | Used by the Ollama backend when no ajq base URL is set. |
| `ANTHROPIC_API_KEY` | secret | Anthropic credential. |
| `OPENAI_API_KEY` | secret | OpenAI credential. |
| `OPENROUTER_API_KEY` | secret | OpenRouter credential. |

Provider API keys are not part of the generic `AJQ_*` config merge. Each provider backend
reads only its own credential environment variable.

## API-key policy

API keys are environment-only. The TOML config rejects credential-looking keys including
`api_key`, `apikey`, and `token`:

```text
config key "api_key" looks like a credential; API keys are env-only (use ANTHROPIC_API_KEY, OPENAI_API_KEY, or OPENROUTER_API_KEY)
```

## Cache paths

| Data | Location under cache root |
|---|---|
| Persistent semantic judgements | `<cache>/judgements/` |
| Local model files | `<cache>/models/` |
| Engine bundles / legacy binary cache | `<cache>/engine/` and legacy `<cache>/bin/` |
| Daemon PID/activity files | Cache-root daemon state files. |

The cache root is `AJQ_CACHE_DIR` when set; otherwise it is the OS user cache directory
joined with `ajq` (for example `~/Library/Caches/ajq` on macOS or `~/.cache/ajq` on
Linux, with `~/.cache/ajq` as a fallback).

## Related

- [Backends](../backends/) — backend-specific defaults and requirements.
- [Command-line interface](../cli/) — flags and subcommands.
