---
title: "Use a larger local model"
linkTitle: "Use a larger model"
weight: 9
description: >
  List, download, and select a larger pinned GGUF model for the local backend.
---

The local backend defaults to `qwen2.5-1.5b`. Use `ajq models` when you want one of the
larger shipped catalog models.

## 1. List catalog models

Run:

```bash
ajq models list
```

Example output before any downloads:

```text
NAME          ACTIVE  INSTALLED  SIZE    RAM
qwen2.5-1.5b  *       no         1.2GiB  ~4 GiB RAM
qwen2.5-3b            no         2.0GiB  ~6 GiB RAM
qwen3-4b              no         2.3GiB  ~8 GiB RAM
```

`ACTIVE` shows the model selected by config/env/defaults. `INSTALLED` shows whether the
GGUF file exists in the ajq cache.

## 2. Download the model you want

Choose a catalog name and pull it:

```bash
ajq models pull qwen2.5-3b
```

The pull uses checksum-pinned public URLs and stores the model under the ajq cache. For a
project-local cache, set `AJQ_CACHE_DIR` before pulling:

```bash
AJQ_CACHE_DIR="$PWD/.ajq-cache" ajq models pull qwen2.5-3b
```

## 3. Select the installed model

Persist the selected local model:

```bash
ajq models use qwen2.5-3b
```

If the model is not installed yet, ajq tells you what to run:

```text
ajq: error: model qwen2.5-3b is not installed; run `ajq models pull qwen2.5-3b` first
```

After the model is installed, `models use` writes the model name to `config.toml`:

```text
active model set to qwen2.5-3b in /Users/you/.config/ajq/config.toml
```

The resulting config entry is:

```toml
model = "qwen2.5-3b"
```

`models use` preserves unrelated config keys, but it does not preserve comments or the
original formatting.

## 4. Run with the local backend

Use the selected model with the local backend:

```bash
printf '{"msg":"urgent billing issue"}' \
  | ajq --backend local '.msg =~ "urgent"'
```

You can override the configured model for one command:

```bash
ajq --backend local --model qwen3-4b '.msg =~ "urgent"'
```

## Related

- [Install ajq](../install/) — provision the local engine and default model.
- [Backends reference](../../reference/backends/) — local backend defaults and cache identity.
- [Configuration reference](../../reference/configuration/) — where `models use` writes.
