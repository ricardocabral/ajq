---
title: "Use cloud backends"
linkTitle: "Use cloud backends"
weight: 5
description: >
  Run semantic queries against Anthropic, OpenAI, or OpenRouter.
---

Use a cloud backend when you want hosted inference instead of the local
`llama-server` backend. Each provider needs an API key in the environment; ajq does not
read API keys from `config.toml`.

## Use Anthropic Claude

1. Export an Anthropic API key:

   ```bash
   export ANTHROPIC_API_KEY="sk-ant-..."
   ```

2. Run a semantic query with `--cloud`:

   ```bash
   printf '{"msg":"urgent billing issue"}' \
     | ajq --cloud --model haiku '.msg =~ "urgent"'
   ```

   `--cloud` is the same as `--backend anthropic`. If you omit `--model`, ajq uses
   `claude-haiku-4-5`. The shipped aliases are `haiku`, `sonnet`, and `opus`.

3. If the key is missing, ajq stops before contacting the provider:

   ```text
   ajq: error: anthropic backend API key is empty; set ANTHROPIC_API_KEY
   ```

## Use OpenAI

1. Export an OpenAI API key:

   ```bash
   export OPENAI_API_KEY="sk-..."
   ```

2. Choose an OpenAI model explicitly:

   ```bash
   printf '{"msg":"urgent billing issue"}' \
     | ajq --backend openai --model gpt-4o-mini '.msg =~ "urgent"'
   ```

3. If you use an OpenAI-compatible proxy, pass its API root with `--base-url`:

   ```bash
   ajq --backend openai --base-url http://127.0.0.1:8000/v1 --model my-model \
     '.msg =~ "urgent"'
   ```

## Use OpenRouter

1. Export an OpenRouter API key:

   ```bash
   export OPENROUTER_API_KEY="sk-or-..."
   ```

2. Choose an OpenRouter model id:

   ```bash
   printf '{"msg":"urgent billing issue"}' \
     | ajq --backend openrouter --model openai/gpt-4o-mini '.msg =~ "urgent"'
   ```

   OpenRouter uses `https://openrouter.ai/api/v1` by default.

## Keep a spending guardrail

Paid backends (`anthropic`, `openai`, and `openrouter`) default to a 100-judgement cap.
Lower it while testing:

```bash
ajq --cloud --max-calls 10 '.[] | select(.msg =~ "urgent")'
```

See [Control semantic costs](../control-costs/) for the full estimate/cap/stats workflow.

## Related

- [Backends reference](../../reference/backends/) — defaults, required settings, and caps.
- [Configuration reference](../../reference/configuration/) — precedence and env-only API keys.
