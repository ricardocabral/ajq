---
title: "Your first ajq pipeline"
linkTitle: "1. Your first pipeline"
weight: 1
description: >
  Run a real jq query through ajq and see deterministic, byte-for-byte output.
---

Run a real jq query through ajq. You'll process a small JSON document, filter it, and
confirm ajq matches jq for ordinary queries. No model, API key, or configuration.

Time: **5 minutes**. Requires ajq on your `PATH`.

## Step 1 — Get ajq

Use the checksum-verifying install script, a manual release download, or build from source
(see [Install ajq](../../how-to/install/) for details):

```bash
curl -fsSL https://raw.githubusercontent.com/ricardocabral/ajq/main/scripts/install.sh | sh
```

Check that it works:

```bash
ajq --version
```

This tutorial uses only deterministic jq, so there's nothing else to set up — no model
download, no API key.

## Step 2 — Run a query

`ajq` reads JSON from standard input and applies the query you pass as its argument.
Let's pipe a small document in and pull out one field:

```bash
printf '{"user":{"name":"Ada","active":true}}' | ajq '.user.name'
```

Output:

```json
"Ada"
```

ajq quotes the string exactly as `jq` would. It uses
[gojq](https://github.com/itchyny/gojq) under the hood.

## Step 3 — Filter a list

Now something more interesting. Here's a document with a list of users; we want the
names of the active ones:

```bash
printf '{"users":[{"name":"Ada","active":true},{"name":"Grace","active":false}]}' \
  | ajq -r '.users[] | select(.active) | .name'
```

Output:

```text
Ada
```

Two things happened:

- `.users[] | select(.active) | .name` is an ordinary jq pipeline — iterate the users,
  keep the active ones, project the name.
- The `-r` flag asked for **raw output**, so the string came out as `Ada` without quotes.

## Step 4 — Prove it's deterministic

Run the exact same command again:

```bash
printf '{"users":[{"name":"Ada","active":true},{"name":"Grace","active":false}]}' \
  | ajq -r '.users[] | select(.active) | .name'
```

You get `Ada` again, byte for byte. Ordinary jq parts are **byte-reproducible**. Semantic
operators, added later, are the only parts that touch a model; everything else stays
deterministic. You'll see that in the [next tutorial](../explain-semantic-plan/).

## Result

- ajq runs real jq queries and produces `jq`-identical output.
- `-r` emits raw strings; without it, ajq emits JSON.
- Ordinary pipelines are deterministic and reproducible.

## Next

- Continue to **[See how ajq plans a semantic query](../explain-semantic-plan/)** to
  write your first fuzzy predicate.
- Browse the [CLI reference](../../reference/cli/) for every flag.
- Read [Split execution](../../explanation/split-execution/) to understand *why* the
  deterministic/semantic split matters.
