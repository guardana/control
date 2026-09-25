---
title: bundle.proto
summary: The messages and enums of bundle.proto as the compiled descriptor declares them, each field with its number, cardinality and type.
type: reference
covers: [api/proto/guardana/control/v1/bundle.proto]
generated: scripts/gen-wire.go
---

# bundle.proto

What `bundle.proto` of package `guardana.control.v1` puts on the wire: every message with its
fields and every enum with its values, as the compiled descriptor declares them. The
descriptor carries no comments, so what a field means, when it is set and what a reader
does with a number it does not know is on [the wire contract](../../contracts.md).

Rendered from the descriptor compiled from `api/proto/guardana/control/v1/bundle.proto`. Rebuild it with
`make docs-gen`; an edit made here does not survive the next run.

## Messages

### PolicyBundleRef

| Field | Number | Cardinality | Type |
| --- | --- | --- | --- |
| `bundle_id` | 1 | optional | `string` |
| `version` | 2 | optional | `string` |
| `digest` | 3 | optional | `string` |
| `created_at` | 4 | optional | `google.protobuf.Timestamp` |

### PolicyBundle

| Field | Number | Cardinality | Type |
| --- | --- | --- | --- |
| `ref` | 1 | optional | [`PolicyBundleRef`](#policybundleref) |
| `canonical` | 2 | optional | `bytes` |
| `signature_alg` | 3 | optional | `string` |
| `signature` | 4 | optional | `bytes` |
| `key_id` | 5 | optional | `string` |
| `max_stale_seconds` | 6 | optional | `int64` |
