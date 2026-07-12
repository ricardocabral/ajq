# Local routing guidance (synthetic evaluation fixture)

When a task operates on JSON or NDJSON, use ordinary `jq` or deterministic code
for exact structural transformations. Consider `ajq` only when the task needs
semantic matching or a bounded semantic classification.

Before semantic execution, inspect `ajq capabilities --json`, then use
`--backend mock` and `--explain`. Never use a real backend without explicit
authority and a finite `--max-calls` value. For sensitive or one-off data,
disclose the semantic-data boundary and use `--no-cache` if ajq is authorized.

Do not claim that ajq provides general standalone extraction or redaction.
