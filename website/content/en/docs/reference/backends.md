---
title: "Backends"
linkTitle: "Backends"
weight: 6
description: >
  Semantic backend names, defaults, required settings, and paid-call caps.
---

Semantic operators require a backend. Pure jq queries do not construct a backend, spawn a
daemon, or make network calls unless a semantic operator is present and a backend is
selected by flags, environment, or config.

## Backend summary

| Backend | Select with | Model default / requirement | Base URL | Credentials | Default `max_calls` |
|---|---|---|---|---|---:|
| `mock` | `--backend mock` | Built-in `ajq-default-model` identity | none | none | `0` (unlimited) |
| `local` | `--backend local` | `qwen2.5-1.5b` unless overridden | Managed loopback `http://127.0.0.1:8081`; explicit `--base-url` for user-trusted servers | managed bearer key (internal) | `0` (unlimited) |
| `ollama` | `--backend ollama` | Required `--model` / config / env | `OLLAMA_HOST` or `http://127.0.0.1:11434` | none | `0` (unlimited) |
| `openai` | `--backend openai` | Required `--model` / config / env | `https://api.openai.com/v1` | `OPENAI_API_KEY` | `100` |
| `openrouter` | `--backend openrouter` | Required `--model` / config / env | `https://openrouter.ai/api/v1` | `OPENROUTER_API_KEY` | `100` |
| `anthropic` | `--backend anthropic` or `--cloud` | `claude-haiku-4-5`; aliases `haiku`, `sonnet`, `opus` | provider default; no user base URL | `ANTHROPIC_API_KEY` | `100` |

`max_calls` counts post-dedup backend judgements. `0` means unlimited. For a
machine-readable static projection of these registered backends, use `ajq
capabilities --json`; it does not resolve configured credentials or runtime
asset state. In that projection Ollama has no `default_base_url`, because its
runtime URL may come from `OLLAMA_HOST` before the documented loopback fallback.

## Mock

The mock backend is deterministic and in-process. It is for examples, tests, fixtures, and
`--explain` estimates. It never contacts a model provider.

## Local

The local backend uses a managed `llama-server` daemon and a GGUF model from the ajq model
catalog. `ajq provision` installs or locates the engine and default model. `ajq models
list|pull|use` manages larger catalog models. Managed daemons are started with
`--api-key`; ajq keeps the generated bearer key beside the PID file in the cache directory
with `0600` permissions and sends it on `/completion` requests.

When ajq owns the managed daemon config, the base URL is an HTTP loopback URL with no path,
query, fragment, or userinfo; bracketed IPv6 loopback is accepted, for example
`http://[::1]:8081`. A health-only listener on the default managed address is a port
conflict, not silent adoption. An explicit `--base-url`, `AJQ_BASE_URL`, or config
`base_url` means you trust that external server; ajq bypasses managed provisioning/daemon
startup and does not send its managed daemon key to that server.

Model identity for cache keys is `local/<catalog-name>` for catalog models, such as
`local/qwen2.5-3b`. Path-like local model overrides use a stable hashed path identity.

## Ollama

The Ollama backend uses Ollama's native structured `/api/chat` endpoint. `--model` is
required unless supplied by `AJQ_MODEL` or config. If no ajq base URL is configured,
`OLLAMA_HOST` is honored before falling back to `http://127.0.0.1:11434`. Host-only forms
such as `127.0.0.1:11434` are accepted and normalized.

## OpenAI and OpenRouter

OpenAI and OpenRouter use OpenAI-compatible `/v1/chat/completions` transport with
structured output. `--model` is required unless supplied by config or `AJQ_MODEL`.

| Backend | Default root | API-key env var |
|---|---|---|
| `openai` | `https://api.openai.com/v1` | `OPENAI_API_KEY` |
| `openrouter` | `https://openrouter.ai/api/v1` | `OPENROUTER_API_KEY` |

A custom `--base-url` is treated as an OpenAI-compatible API root.

## Anthropic

`--cloud` selects the Anthropic backend and is equivalent to `--backend anthropic`.
The default model is `claude-haiku-4-5`. Model aliases resolve as follows:

| Alias | Model id |
|---|---|
| `haiku` | `claude-haiku-4-5` |
| `sonnet` | `claude-sonnet-5` |
| `opus` | `claude-opus-4-8` |

Anthropic credentials come only from `ANTHROPIC_API_KEY`.

## Paid backend defaults

The paid/remote backends (`anthropic`, `openai`, and `openrouter`) default to
`max_calls = 100` unless a flag, env var, or config value overrides it. Local, Ollama, and
mock default to unlimited.

## Related

- [Configuration](../configuration/) — precedence and environment variables.
- [Control semantic costs](../../how-to/control-costs/) — estimate, cap, and inspect calls.
