---
title: "Semantic functions"
linkTitle: "Semantic functions"
weight: 3
description: >
  The shipped semantic vocabulary, =~/!~ sugar, arities, return types, and current limits.
---

Semantic operators are registered as jq functions, so gojq parses them inside ordinary jq
constructs. ajq adds only the `=~` and `!~` surface sugar before parsing.

## Function vocabulary

| Function | Form | Kind | Returns | Execution status |
|---|---|---|---|---|
| `sem_match` | `sem_match(value; "spec")` | predicate | boolean | Shipped |
| `sem_classify` | `sem_classify(value; "a"; "b"; …)` | bounded value | one label from the given labels | Shipped |
| `sem_extract` | `sem_extract(value; "what")` | unbounded value | string | Registered/planned; current execution errors until safe unbounded fallback ships |
| `sem_score` | `sem_score(value; "spec")` | unbounded value | number | Registered/planned; current execution errors until safe unbounded fallback ships |
| `sem_norm` | `sem_norm(value; "canonicalization spec")` | unbounded value | string | Registered/planned; current execution support is limited; avoid in user workflows |
| `sem_redact` | `sem_redact(value; "redaction spec")` | unbounded value | string | Registered/planned; current execution errors until safe unbounded fallback ships |

The **spec** is a literal string in the query. Stream **data** is structurally fenced by
the selected backend's output constraints.

## Kinds

| Kind | Meaning |
|---|---|
| Predicate | Returns `true` or `false`; typically used in `select` or `if`. |
| Bounded value | Returns one of a finite set declared at the call site. |
| Unbounded value | Returns a string or number with no finite output set. These are the shapes still limited in the current executor. |

## Variadic implicit `.`

Every operator accepts both an explicit value and an implicit one. When the value argument
is omitted, it defaults to `.`:

```jq
sem_match(.field; "spec")   # explicit value
sem_match("spec")           # value defaults to .
```

The implicit form is what makes raw-line mode ergonomic.

## The `=~` and `!~` sugar

| Surface | Desugars to |
|---|---|
| `X =~ "spec"` | `sem_match(X; "spec")` |
| `X !~ "spec"` | `sem_match(X; "spec") | not` |

The desugaring is performed by a jq-aware lexer, not a regular expression.

```jq
.users[] | select(.feedback =~ "angry/frustrated") | .id
# ≡
.users[] | select(sem_match(.feedback; "angry/frustrated")) | .id
```

## Return-type discipline

Each semantic operator has a fixed output type: boolean, enum label, string, or number.
Backends constrain model output with GBNF grammar or provider structured-output/schema
features, so downstream jq receives a value of the expected shape when execution succeeds.

For `sem_classify`, the enum is exactly the label arguments at the call site:

```jq
sem_classify(.text; "billing"; "bug"; "feature")
```

That expression returns one of `"billing"`, `"bug"`, or `"feature"`.

## Scenario matrix

| Scenario | Invocation | Status |
|---|---|---|
| Fuzzy filter | `select(.msg =~ "auth failure")` | Shipped |
| Raw-line fuzzy filter | `select(. =~ "stack trace")` with `-R` | Shipped |
| Routing / labeling | `{route: sem_classify(.text; "billing"; "bug"; "feature")}` | Shipped |
| Typed extraction | `sem_extract(.raw; "years")` | Planned execution support; currently errors |
| Semantic scoring | `sem_score(.review; "positivity 0-1")` | Planned execution support; currently errors |
| Semantic redaction | `.notes |= sem_redact(.; "PII")` | Planned execution support; currently errors |

## Current grammar scope

There is no custom jq grammar. `@classify(...)`-style `@` forms and infix transform
operators such as `~>` do not parse in gojq and are not shipped. Use the function-core
forms above.

## Related

- [Write a semantic filter](../../how-to/semantic-filter/) — task recipes using shipped forms.
- [Split execution](../../explanation/split-execution/) — why ajq keeps jq as the parser.
