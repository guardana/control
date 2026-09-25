---
title: decision.proto
summary: The messages and enums of decision.proto as the compiled descriptor declares them, each field with its number, cardinality and type.
type: reference
covers: [api/proto/guardana/control/v1/decision.proto]
generated: scripts/gen-wire.go
---

# decision.proto

What `decision.proto` of package `guardana.control.v1` puts on the wire: every message with its
fields and every enum with its values, as the compiled descriptor declares them. The
descriptor carries no comments, so what a field means, when it is set and what a reader
does with a number it does not know is on [the wire contract](../../contracts.md).

Rendered from the descriptor compiled from `api/proto/guardana/control/v1/decision.proto`. Rebuild it with
`make docs-gen`; an edit made here does not survive the next run.

## Messages

### Obligation

| Field | Number | Cardinality | Type |
| --- | --- | --- | --- |
| `type` | 1 | optional | `string` |
| `params` | 2 | map | map<`string`, `string`> |
| `advisory` | 3 | optional | `bool` |

### Decision

| Field | Number | Cardinality | Type |
| --- | --- | --- | --- |
| `schema_version` | 13 | optional | `string` |
| `decision_id` | 1 | optional | `string` |
| `request_id` | 2 | optional | `string` |
| `action_digest` | 16 | optional | `string` |
| `policy_bundle_digest` | 3 | optional | `string` |
| `policy_rule_ids` | 4 | repeated | `string` |
| `verdict` | 5 | optional | [`Verdict`](common.md#verdict) |
| `reason_codes` | 6 | repeated | `string` |
| `obligations` | 7 | repeated | [`Obligation`](#obligation) |
| `expires_at` | 8 | optional | `google.protobuf.Timestamp` |
| `decision_latency_us` | 9 | optional | `int64` |
| `pdp_type` | 10 | optional | `string` |
| `pdp_instance` | 11 | optional | `string` |
| `pdp_version` | 12 | optional | `string` |
| `enforcement_mode` | 14 | optional | [`EnforcementMode`](common.md#enforcementmode) |
| `policy_freshness` | 15 | optional | [`PolicyFreshness`](common.md#policyfreshness) |
| `policy_loaded_at` | 17 | optional | `google.protobuf.Timestamp` |
| `decided_at` | 18 | optional | `google.protobuf.Timestamp` |
