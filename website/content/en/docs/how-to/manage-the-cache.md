---
title: "Manage the judgement cache"
linkTitle: "Manage the cache"
weight: 7
description: >
  Inspect, bypass, and clear ajq's persistent semantic judgement cache.
---

ajq stores successful semantic judgements on disk so repeated runs over the same
op/spec/model/value identity can skip backend calls.

## See where the cache lives

Run:

```bash
ajq cache status
```

Example output:

```text
location: /Users/you/Library/Caches/ajq/judgements
entries: 0
bytes: 0
```

Set `AJQ_CACHE_DIR` when you want an isolated cache for a project or test:

```bash
AJQ_CACHE_DIR="$PWD/.ajq-cache" ajq cache status
```

## Re-run a semantic query for free

Run a semantic query once with stats:

```bash
printf '[{"msg":"urgent"},{"msg":"urgent"},{"msg":"other"}]' \
  | ajq --backend mock --stats '.[] | select(.msg =~ "urgent") | .msg'
```

The first run performs one backend judgement per distinct value:

```text
ajq stats:
  harvested_judgements: 3
  post_dedup_backend_calls: 2
  cache_hits: 0
```

Run the same command again with the same cache root. The output is the same, but the
judgements are cache hits:

```text
ajq stats:
  harvested_judgements: 3
  post_dedup_backend_calls: 0
  cache_hits: 3
```

## Bypass the cache for one run

Use `--no-cache` when the judged values should not be read from or written to disk:

```bash
printf '[{"msg":"urgent"},{"msg":"other"}]' \
  | ajq --backend mock --no-cache --stats '.[] | select(.msg =~ "urgent") | .msg'
```

You can also set `no_cache = true` in `config.toml`; use the flag when you only need the
bypass once.

## Clear cached judgements

Run:

```bash
ajq cache clear
```

Example output:

```text
location: /Users/you/Library/Caches/ajq/judgements
cleared_entries: 2
freed_bytes: 422
```

`cache clear` removes judgement entries only. It does not remove provisioned engines or
local GGUF models.

## Privacy note

Cache files contain the values that semantic operators judged. Do not share the cache
folder if those values are sensitive. Use `--no-cache` or a disposable `AJQ_CACHE_DIR` for
one-off processing of private data.

## Related

- [Control semantic costs](../control-costs/) — use cache hits alongside caps and stats.
- [Configuration reference](../../reference/configuration/) — `AJQ_CACHE_DIR` and `no_cache`.
