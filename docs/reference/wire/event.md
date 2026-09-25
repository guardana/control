---
title: event.proto
summary: The messages and enums of event.proto as the compiled descriptor declares them, each field with its number, cardinality and type.
type: reference
covers: [api/proto/guardana/control/v1/event.proto]
generated: scripts/gen-wire.go
---

# event.proto

What `event.proto` of package `guardana.control.v1` puts on the wire: every message with its
fields and every enum with its values, as the compiled descriptor declares them. The
descriptor carries no comments, so what a field means, when it is set and what a reader
does with a number it does not know is on [the wire contract](../../contracts.md).

Rendered from the descriptor compiled from `api/proto/guardana/control/v1/event.proto`. Rebuild it with
`make docs-gen`; an edit made here does not survive the next run.

## Messages

### Event

| Field | Number | Cardinality | Type |
| --- | --- | --- | --- |
| `event_id` | 1 | optional | `string` |
| `kind` | 2 | optional | [`EventKind`](#eventkind) |
| `request_id` | 3 | optional | `string` |
| `run_id` | 4 | optional | `string` |
| `project_id` | 5 | optional | `string` |
| `tenant_id` | 6 | optional | `string` |
| `occurred_at` | 7 | optional | `google.protobuf.Timestamp` |
| `schema_version` | 8 | optional | `string` |
| `enforcement_mode` | 9 | optional | [`EnforcementMode`](common.md#enforcementmode) |
| `execution_id` | 10 | optional | `string` |
| `proposed` | 20 | oneof `payload` | [`ActionEnvelope`](action_envelope.md#actionenvelope) |
| `decision` | 21 | oneof `payload` | [`Decision`](decision.md#decision) |
| `approval` | 22 | oneof `payload` | [`Approval`](approval.md#approval) |
| `result` | 23 | oneof `payload` | [`ActionResult`](result.md#actionresult) |
| `finding` | 24 | oneof `payload` | [`Finding`](finding.md#finding) |
| `policy` | 25 | oneof `payload` | [`PolicyBundleRef`](bundle.md#policybundleref) |
| `prev_event_id` | 30 | optional | `string` |
| `prev_event_digest` | 31 | optional | `string` |

## Enums

### EventKind

| Value | Number |
| --- | --- |
| `EVENT_KIND_UNSPECIFIED` | 0 |
| `EVENT_KIND_ACTION_PROPOSED` | 1 |
| `EVENT_KIND_POLICY_DECIDED` | 2 |
| `EVENT_KIND_APPROVAL_REQUESTED` | 3 |
| `EVENT_KIND_APPROVAL_DECIDED` | 4 |
| `EVENT_KIND_ACTION_STARTED` | 5 |
| `EVENT_KIND_ACTION_COMPLETED` | 6 |
| `EVENT_KIND_ACTION_FAILED` | 7 |
| `EVENT_KIND_ACTION_BLOCKED` | 8 |
| `EVENT_KIND_APPROVAL_EXPIRED` | 9 |
| `EVENT_KIND_FINDING_RAISED` | 10 |
| `EVENT_KIND_POLICY_RELOADED` | 11 |
