# Phase 2 benchmark fixtures

Tiny, human-readable representative inputs for the ajq Phase 2.5 bench harness
(`internal/bench`). These exist purely for illustration and manual smoke runs —
the harness itself generates its datasets in-memory (see
`internal/bench/workload.go`), so **no large data files are committed**.

- `predicate_array.json` — a small JSON array for semantic-predicate and
  value-op queries (`sem_match`, `sem_score`, `sem_norm`, `sem_classify`).
- `predicate_stream.ndjson` — the same records as NDJSON, one object per line,
  for per-record streaming throughput.

Example manual runs (fake mode, deterministic — no model required):

```sh
ajq --backend mock '.[] | select(sem_match(.msg; "urgent")) | .id' \
  < testdata/bench/predicate_array.json

ajq --backend mock 'select(sem_match(.msg; "urgent")) | .id' \
  < testdata/bench/predicate_stream.ndjson
```
