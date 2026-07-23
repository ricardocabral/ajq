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
printf '[{"id":1,"text":"billing question"},{"id":2,"text":"bug report"}]' \
  | ajq --backend mock -c '.[] | {id, route: sem_classify(.text; "billing"; "bug"; "feature")}'
```

Expected output with the mock backend:

```json
{"id":1,"route":"billing"}
{"id":2,"route":"bug"}
```

`sem_classify` returns exactly one of the labels from the call site. Keep labels short,
mutually exclusive, and stable because they become part of your output contract.

## Classify NDJSON without whole-stream buffering

ajq processes newline-delimited JSON as independent input frames, so it can classify streams
without buffering the whole input. The default supported semantic path may resolve several
frames in a byte-budgeted window; use `--stream` when each frame needs the lowest possible
first-result latency.

```bash
printf '{"id":1,"text":"billing question"}\n{"id":2,"text":"bug report"}\n' \
  | ajq --backend mock -c '{id, route: sem_classify(.text; "billing"; "bug"; "feature")}'
```

This emits one compact JSON object per input record, in input order, which is convenient for
queues, logs, and shell pipelines. See [Process an NDJSON stream](../process-ndjson/) for
the windowing and `--stream` trade-offs.

## Route webhook events with bounded labels

If a webhook receiver writes each event body as NDJSON, add a stable route before passing
the stream to downstream workers:

```bash
printf '{"event":"invoice.payment_failed","data":{"message":"billing payment failed"}}\n{"event":"issue.created","data":{"message":"bug report: export crashes"}}\n' \
  | ajq --backend mock -c '. + {route: sem_classify(.data.message; "billing"; "bug"; "other")}'
```

The command preserves each webhook event and adds a `route` value drawn from the three
labels in the query. Replace the mock backend with a capped real backend when the event
text requires semantic classification.

## Switch to a real classifier backend

After the mock command produces the shape you need, run the same query with a real
backend:

```bash
# Local model after `ajq provision`
printf '{"id":1,"text":"billing question"}\n{"id":2,"text":"bug report"}\n' \
  | ajq --backend local --max-calls 25 -c '{id, route: sem_classify(.text; "billing"; "bug"; "feature")}'

# OpenAI-compatible backend, using OPENAI_API_KEY from the environment
printf '{"id":1,"text":"billing question"}\n{"id":2,"text":"bug report"}\n' \
  | ajq --backend openai --model gpt-4.1-mini --max-calls 25 \
      -c '{id, route: sem_classify(.text; "billing"; "bug"; "feature")}'
```

For paid backends, keep `--max-calls` at or below your budget. Backend-specific defaults
are listed in the [backends reference](../../reference/backends/#paid-backend-defaults).

## Account for repeated values and cache hits

Semantic classification is cache-keyed by backend, model, operation, label set, and input
value. If many records contain the same text, ajq deduplicates the judgements before it
calls the backend, and later runs can reuse the persistent cache.

Estimate the distinct judgement count first:

```bash
printf '{"text":"billing question"}\n{"text":"billing question"}\n{"text":"bug report"}\n' \
  | ajq --explain '{route: sem_classify(.text; "billing"; "bug"; "feature")}'
```

Then add `--stats` to the real run when you want to see post-dedup backend calls and cache
hits on stderr.

## Related

- [Process an NDJSON stream](../process-ndjson/) — input framing for JSON, NDJSON, raw lines, and null input.
- [Semantic functions reference](../../reference/semantic-functions/) — bounded classification semantics and limitations.
- [Control semantic costs](../control-costs/) — combine estimates, caps, stats, and cache behavior.
- [Backends reference](../../reference/backends/) — choose local, mock, Ollama, OpenAI, OpenRouter, or Anthropic.
