---
title: action_envelope.proto
summary: The messages and enums of action_envelope.proto as the compiled descriptor declares them, each field with its number, cardinality and type.
type: reference
covers: [api/proto/guardana/control/v1/action_envelope.proto]
generated: scripts/gen-wire.go
---

# action_envelope.proto

What `action_envelope.proto` of package `guardana.control.v1` puts on the wire: every message with its
fields and every enum with its values, as the compiled descriptor declares them. The
descriptor carries no comments, so what a field means, when it is set and what a reader
does with a number it does not know is on [the wire contract](../../contracts.md).

Rendered from the descriptor compiled from `api/proto/guardana/control/v1/action_envelope.proto`. Rebuild it with
`make docs-gen`; an edit made here does not survive the next run.

## Messages

### Principal

| Field | Number | Cardinality | Type |
| --- | --- | --- | --- |
| `id` | 1 | optional | `string` |
| `type` | 2 | optional | `string` |
| `authn_strength` | 3 | optional | `string` |
| `tenant_id` | 4 | optional | `string` |
| `attributes` | 5 | map | map<`string`, `string`> |

### Agent

| Field | Number | Cardinality | Type |
| --- | --- | --- | --- |
| `id` | 1 | optional | `string` |
| `instance_id` | 2 | optional | `string` |
| `framework` | 3 | optional | `string` |
| `version` | 4 | optional | `string` |
| `model_ref` | 5 | optional | `string` |

### Delegation

| Field | Number | Cardinality | Type |
| --- | --- | --- | --- |
| `from` | 1 | optional | `string` |
| `to` | 2 | optional | `string` |
| `scopes` | 3 | repeated | `string` |
| `reason` | 4 | optional | `string` |
| `issued_at` | 5 | optional | `google.protobuf.Timestamp` |
| `expires_at` | 6 | optional | `google.protobuf.Timestamp` |

### Action

| Field | Number | Cardinality | Type |
| --- | --- | --- | --- |
| `kind` | 1 | optional | `string` |
| `name` | 2 | optional | `string` |
| `protocol` | 3 | optional | `string` |
| `effect` | 4 | optional | [`EffectClass`](common.md#effectclass) |
| `provider` | 5 | optional | `string` |

### Resource

| Field | Number | Cardinality | Type |
| --- | --- | --- | --- |
| `type` | 1 | optional | `string` |
| `id` | 2 | optional | `string` |
| `tenant_id` | 3 | optional | `string` |
| `environment` | 4 | optional | `string` |
| `labels` | 5 | map | map<`string`, `string`> |

### Destination

| Field | Number | Cardinality | Type |
| --- | --- | --- | --- |
| `trust_zone` | 1 | optional | [`TrustZone`](common.md#trustzone) |
| `host` | 3 | optional | `string` |

### DataLabels

| Field | Number | Cardinality | Type |
| --- | --- | --- | --- |
| `sensitivities` | 1 | repeated | [`Sensitivity`](common.md#sensitivity) |
| `sources` | 2 | repeated | `string` |
| `contains_secrets` | 3 | optional | `bool` |

### Arguments

| Field | Number | Cardinality | Type |
| --- | --- | --- | --- |
| `canonical_hash` | 1 | optional | `string` |
| `redacted_preview` | 2 | optional | `string` |
| `schema_ref` | 3 | optional | `string` |
| `redaction_profile` | 4 | optional | `string` |

### RunContext

| Field | Number | Cardinality | Type |
| --- | --- | --- | --- |
| `session_id` | 1 | optional | `string` |
| `run_id` | 2 | optional | `string` |
| `step_id` | 3 | optional | `string` |
| `risk` | 4 | optional | `string` |
| `budgets` | 5 | map | map<`string`, `int64`> |
| `tags` | 6 | repeated | `string` |

### ActionEnvelope

| Field | Number | Cardinality | Type |
| --- | --- | --- | --- |
| `schema_version` | 1 | optional | `string` |
| `request_id` | 2 | optional | `string` |
| `trace_id` | 3 | optional | `string` |
| `span_id` | 4 | optional | `string` |
| `occurred_at` | 5 | optional | `google.protobuf.Timestamp` |
| `project_id` | 6 | optional | `string` |
| `tenant_id` | 7 | optional | `string` |
| `environment` | 8 | optional | `string` |
| `principal` | 9 | optional | [`Principal`](#principal) |
| `agent` | 10 | optional | [`Agent`](#agent) |
| `delegation` | 11 | repeated | [`Delegation`](#delegation) |
| `action` | 12 | optional | [`Action`](#action) |
| `resource` | 13 | optional | [`Resource`](#resource) |
| `destination` | 14 | optional | [`Destination`](#destination) |
| `data` | 15 | optional | [`DataLabels`](#datalabels) |
| `arguments` | 16 | optional | [`Arguments`](#arguments) |
| `context` | 17 | optional | [`RunContext`](#runcontext) |
