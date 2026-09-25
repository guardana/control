---
title: approval.proto
summary: The messages and enums of approval.proto as the compiled descriptor declares them, each field with its number, cardinality and type.
type: reference
covers: [api/proto/guardana/control/v1/approval.proto]
generated: scripts/gen-wire.go
---

# approval.proto

What `approval.proto` of package `guardana.control.v1` puts on the wire: every message with its
fields and every enum with its values, as the compiled descriptor declares them. The
descriptor carries no comments, so what a field means, when it is set and what a reader
does with a number it does not know is on [the wire contract](../../contracts.md).

Rendered from the descriptor compiled from `api/proto/guardana/control/v1/approval.proto`. Rebuild it with
`make docs-gen`; an edit made here does not survive the next run.

## Messages

### Approval

| Field | Number | Cardinality | Type |
| --- | --- | --- | --- |
| `schema_version` | 12 | optional | `string` |
| `approval_id` | 1 | optional | `string` |
| `request_id` | 2 | optional | `string` |
| `action_digest` | 3 | optional | `string` |
| `policy_bundle_digest` | 4 | optional | `string` |
| `state` | 5 | optional | [`ApprovalState`](#approvalstate) |
| `approver_id` | 6 | optional | `string` |
| `reason` | 7 | optional | `string` |
| `requested_at` | 8 | optional | `google.protobuf.Timestamp` |
| `decided_at` | 9 | optional | `google.protobuf.Timestamp` |
| `expires_at` | 10 | optional | `google.protobuf.Timestamp` |
| `multi_use` | 13 | optional | `bool` |

## Enums

### ApprovalState

| Value | Number |
| --- | --- |
| `APPROVAL_STATE_UNSPECIFIED` | 0 |
| `APPROVAL_STATE_PENDING` | 1 |
| `APPROVAL_STATE_APPROVED` | 2 |
| `APPROVAL_STATE_REJECTED` | 3 |
| `APPROVAL_STATE_EXPIRED` | 4 |
