---
title: "Filter JSON by meaning"
linkTitle: "Filter by meaning"
weight: 3
description: >
  Find JSON records by intent or topic with ajq semantic filters, starting with a no-network mock backend and then switching to a real model.
---

Use this when exact string matching is too brittle: support tickets that say the same
thing in different words, log messages that imply the same failure, or records where the
field name is known but the wording is messy.

ajq keeps the surrounding jq pipeline deterministic. Only the explicit semantic predicate
(`=~`, `!~`, or `sem_match`) asks a backend for a judgement.

## Try the query with no network

The mock backend is deterministic and in-process, so it needs no model, network, or API
key. It is useful for checking the jq shape before paying for model calls:

```bash
printf '[{"id":1,"msg":"payment failed during checkout"},{"id":2,"msg":"new avatar uploaded"}]' \
  | ajq --backend mock -c '.[] | select(.msg =~ "payment failed") | {id, msg}'
```

Expected output with the mock backend:

```json
{"id":1,"msg":"payment failed during checkout"}
```

The predicate `.msg =~ "payment failed"` is shorthand for
`sem_match(.msg; "payment failed")`. Everything else is ordinary jq.

## Run the same filter on a real backend

After the command shape is correct, choose a backend that can make real semantic
judgements:

```bash
# Managed local backend after `ajq provision`
printf '[{"id":1,"msg":"card declined at checkout"},{"id":2,"msg":"profile photo changed"}]' \
  | ajq --backend local --max-calls 10 -c '.[] | select(.msg =~ "payment failure") | {id, msg}'

# Anthropic cloud backend, using ANTHROPIC_API_KEY from the environment
printf '[{"id":1,"msg":"card declined at checkout"},{"id":2,"msg":"profile photo changed"}]' \
  | ajq --cloud --max-calls 10 -c '.[] | select(.msg =~ "payment failure") | {id, msg}'
```

See the [backends reference](../../reference/backends/#paid-backend-defaults) for default
caps. Setting a smaller `--max-calls` while you tune a query makes accidental large runs
fail before crossing your budget.

## Estimate, cap, and cache the work

Before running against a real model, ask ajq to explain the semantic plan:

```bash
printf '[{"msg":"payment failed"},{"msg":"payment failed"},{"msg":"avatar uploaded"}]' \
  | ajq --explain '.[] | select(.msg =~ "payment failure") | .msg'
```

The value to budget against is `post_dedup_judgements`: repeated values are judged once
per backend/model/spec/value cache key. Successful judgements are stored in the persistent
cache, so rerunning the same query can hit the cache instead of the backend. Use
`--no-cache` only when you intentionally want to avoid reading or writing cached
judgements.

## Keep filters within shipped semantic shapes

For production filtering, prefer `=~`, `!~`, or `sem_match`. Standalone semantic
extraction and redaction are registered but currently unsupported, so do not build a
filter that assumes ajq can extract arbitrary fields or redact text as a standalone
transform.

## Related

- [Semantic functions reference](../../reference/semantic-functions/) — exact function forms, return types, and current limits.
- [Backends reference](../../reference/backends/) — backend names, model requirements, credentials, and default caps.
- [Estimate model calls](../estimate-model-calls/) — read `--explain` estimates before executing.
- [Manage the judgement cache](../manage-the-cache/) — inspect, bypass, or clear cached judgements.
