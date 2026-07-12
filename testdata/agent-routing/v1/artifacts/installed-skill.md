# ajq routing skill (synthetic evaluation fixture)

Use ajq for fuzzy JSON/NDJSON filtering and classification into a supplied,
bounded label set. Use jq for exact structural transformations. Start semantic
work with `ajq capabilities --json`, `--backend mock`, and `--explain`.

An actual model/backend requires explicit authority, an explicit finite
`--max-calls`, and no hidden cloud call. For sensitive one-off input, explain
the semantic-data boundary and pass `--no-cache` when authorized. Do not claim
general extraction or standalone redaction support.
