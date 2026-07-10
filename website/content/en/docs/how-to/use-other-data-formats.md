---
title: "Use ajq with other data formats"
linkTitle: "Other data formats"
weight: 3
description: >
  Feed ajq JSON produced by CLI, YAML, XML, TOML, or binary adapters.
---

ajq's core input modes stay small on purpose: JSON, NDJSON, raw strings, and
null input. When the source data is another format, put a format adapter before
ajq and let ajq work on the JSON it emits.

This keeps pure jq-compatible ajq pipelines deterministic while still letting
you use the existing jq ecosystem around it. Tools such as `jc`, `yq`/`xq`, and
`fq` are commonly used alongside jq for this kind of conversion.

## Convert command output with `jc`

Use `jc` when a system command prints text that you want to query as JSON:

```bash
df -h | jc --df | ajq -c '.[] | {filesystem, size, used_percent}'
```

Once `jc` emits JSON, ajq sees ordinary JSON values. Add semantic filters only
when you explicitly want model-backed judgement:

```bash
ps aux | jc --ps | ajq --backend mock -c '.[] | select(.command =~ "database server")'
```

## Convert YAML, XML, or TOML with `yq` and `xq`

Use `yq` for YAML or TOML files that should flow into an ajq query:

```bash
yq -o=json '.services' docker-compose.yml \
  | ajq -c 'to_entries[] | {name: .key, image: .value.image}'
```

Some `yq` distributions also provide `xq` for XML-to-JSON workflows:

```bash
xq '.rss.channel.item' feed.xml \
  | ajq -c '.[] | {title, link}'
```

Check your installed `yq`/`xq` command's flags, because option names vary by
implementation. The important contract is that the adapter writes JSON to
stdout before ajq starts.

## Convert binary-derived data with `fq`

Use `fq` when a binary or structured file format can be projected as JSON. For
decoded values, use `-V` or `tovalue` when you need fq to write the JSON value
instead of its display tree.

```bash
fq '.frames[0:10] | map(tobytesrange.start)' file.mp3 \
  | ajq -c '{frame_count: length, first_offset: .[0]}'
```

Keep the format-specific parsing in `fq`; keep jq and semantic operations in
ajq after the JSON boundary.

## Choose the ajq input mode after conversion

Most adapters emit a single JSON value, so the default ajq input mode is enough.
If an adapter emits one JSON value per line, ajq processes it as NDJSON:

```bash
some-adapter --json-lines input.dat \
  | ajq -c 'select(.level == "error")'
```

If you only have raw text and do not want a JSON adapter, use ajq raw-input mode
instead:

```bash
some-command | ajq -R -r 'select(test("error"))'
```

## Related

- [Process an NDJSON stream](../process-ndjson/) - ajq input framing and raw
  line mode.
- [Input and output modes reference](../../reference/io-modes/) - every input
  and output flag.
