---
title: result.proto
summary: The messages and enums of result.proto as the compiled descriptor declares them, each field with its number, cardinality and type.
type: reference
covers: [api/proto/guardana/control/v1/result.proto]
generated: scripts/gen-wire.go
---

# result.proto

What `result.proto` of package `guardana.control.v1` puts on the wire: every message with its
fields and every enum with its values, as the compiled descriptor declares them. The
descriptor carries no comments, so what a field means, when it is set and what a reader
does with a number it does not know is on [the wire contract](../../contracts.md).

Rendered from the descriptor compiled from `api/proto/guardana/control/v1/result.proto`. Rebuild it with
`make docs-gen`; an edit made here does not survive the next run.

## Messages

### ActionResult

| Field | Number | Cardinality | Type |
| --- | --- | --- | --- |
| `schema_version` | 12 | optional | `string` |
| `request_id` | 1 | optional | `string` |
| `execution_id` | 2 | optional | `string` |
| `status` | 3 | optional | [`ResultStatus`](#resultstatus) |
| `started_at` | 4 | optional | `google.protobuf.Timestamp` |
| `ended_at` | 5 | optional | `google.protobuf.Timestamp` |
| `tool_protocol_status` | 6 | optional | `string` |
| `result_schema_valid` | 7 | optional | `bool` |
| `result_hash` | 8 | optional | `string` |
| `redacted_result_preview` | 9 | optional | `string` |
| `retryable` | 10 | optional | `bool` |
| `side_effect_confirmation` | 11 | optional | `string` |
| `executed_action_digest` | 13 | optional | `string` |
| `redaction_profile` | 14 | optional | `string` |

## Enums

### ResultStatus

| Value | Number |
| --- | --- |
| `RESULT_STATUS_UNSPECIFIED` | 0 |
| `RESULT_STATUS_SUCCESS` | 1 |
| `RESULT_STATUS_FAILURE` | 2 |
| `RESULT_STATUS_TIMEOUT` | 3 |
| `RESULT_STATUS_CANCELLED` | 4 |
| `RESULT_STATUS_BLOCKED` | 5 |
| `RESULT_STATUS_UNKNOWN` | 6 |
