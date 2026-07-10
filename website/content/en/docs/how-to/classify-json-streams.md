---
title: "Classify JSON and NDJSON streams"
linkTitle: "Classify streams"
weight: 4
description: >
  Route JSON or NDJSON records into fixed semantic labels with sem_classify while preserving deterministic jq output shapes.
---

Use `sem_classify` when each record needs one label from a small list: support route,
incident type, moderation bucket, or review theme. The output is bounded to the labels you
write in the query, which makes it safer for downstream jq and shell pipelines than free
text generation.

## Classify a JSON array with no network

Start with `--backend mock` to validate the jq expression without a model, provider, or
API key:

```bash
printf '[{"id":1,"text":"refund requested"},{"id":2,"text":"button is broken"}]' \
  | ajq --backend mock -c '.[] | {id, route: sem_classify(.text; "billing"; "bug"; "feature")}'
```

Expected output with the mock backend:

```json
{"id":1,"route":"billing"}
{"id":2,"route":"bug"}
```

`sem_classify` returns exactly one of the labels from the call site. Keep labels short,
mutually exclusive, and stable because they become part of your output contract.

## Classify NDJSON one record at a time

ajq processes newline-delimited JSON as independent input frames, so it can classify
streams without buffering the whole input:

```bash
printf '{"id":1,"text":"refund requested"}\n{"id":2,"text":"button is broken"}\n' \
  | ajq --backend mock -c '{id, route: sem_classify(.text; "billing"; "bug"; "feature")}'
```

This emits one compact JSON object per input record, which is convenient for queues,
logs, and shell pipelines.

## Switch to a real classifier backend

After the mock command produces the shape you need, run the same query with a real
backend:

```bash
# Local model after `ajq provision`
printf '{"id":1,"text":"refund requested"}\n{"id":2,"text":"button is broken"}\n' \
  | ajq --backend local --max-calls 25 -c '{id, route: sem_classify(.text; "billing"; "bug"; "feature")}'

# OpenAI-compatible backend, using OPENAI_API_KEY from the environment
printf '{"id":1,"text":"refund requested"}\n{"id":2,"text":"button is broken"}\n' \
  | ajq --backend openai --model gpt-4.1-mini --max-calls 25 \
      -c '{id, route: sem_classify(.text; "billing"; "bug"; "feature")}'
```

For paid backends, keep `--max-calls` at or below your budget. Paid/cloud backends also
have a default 100-call guardrail when you do not set one explicitly.

## Account for repeated values and cache hits

Semantic classification is cache-keyed by backend, model, operation, label set, and input
value. If many records contain the same text, ajq deduplicates the judgements before it
calls the backend, and later runs can reuse the persistent cache.

Estimate the distinct judgement count first:

```bash
printf '{"text":"refund requested"}\n{"text":"refund requested"}\n{"text":"button is broken"}\n' \
  | ajq --explain '{route: sem_classify(.text; "billing"; "bug"; "feature")}'
```

Then add `--stats` to the real run when you want to see post-dedup backend calls and cache
hits on stderr.

## Related

- [Process an NDJSON stream](../process-ndjson/) — input framing for JSON, NDJSON, raw lines, and null input.
- [Semantic functions reference](../../reference/semantic-functions/) — bounded classification semantics and limitations.
- [Control semantic costs](../control-costs/) — combine estimates, caps, stats, and cache behavior.
- [Backends reference](../../reference/backends/) — choose local, mock, Ollama, OpenAI, OpenRouter, or Anthropic.
