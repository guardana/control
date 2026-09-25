---
title: finding.proto
summary: The messages and enums of finding.proto as the compiled descriptor declares them, each field with its number, cardinality and type.
type: reference
covers: [api/proto/guardana/control/v1/finding.proto]
generated: scripts/gen-wire.go
---

# finding.proto

What `finding.proto` of package `guardana.control.v1` puts on the wire: every message with its
fields and every enum with its values, as the compiled descriptor declares them. The
descriptor carries no comments, so what a field means, when it is set and what a reader
does with a number it does not know is on [the wire contract](../../contracts.md).

Rendered from the descriptor compiled from `api/proto/guardana/control/v1/finding.proto`. Rebuild it with
`make docs-gen`; an edit made here does not survive the next run.

## Messages

### Finding

| Field | Number | Cardinality | Type |
| --- | --- | --- | --- |
| `finding_id` | 1 | optional | `string` |
| `rule_id` | 2 | optional | `string` |
| `rule_version` | 3 | optional | `string` |
| `severity` | 4 | optional | [`FindingSeverity`](#findingseverity) |
| `verdict` | 5 | optional | [`FindingVerdict`](#findingverdict) |
| `request_id` | 6 | optional | `string` |
| `run_id` | 7 | optional | `string` |
| `evidence_refs` | 8 | repeated | `string` |
| `framework_mappings` | 9 | repeated | `string` |
| `recommended_action` | 10 | optional | `string` |
| `source` | 12 | optional | [`FindingSource`](#findingsource) |
| `confidence_permille` | 11 | optional, presence tracked | `uint32` |

## Enums

### FindingVerdict

| Value | Number |
| --- | --- |
| `FINDING_VERDICT_UNSPECIFIED` | 0 |
| `FINDING_VERDICT_CONFIRMED` | 1 |
| `FINDING_VERDICT_SUSPECTED` | 2 |
| `FINDING_VERDICT_INDETERMINATE` | 3 |

### FindingSeverity

| Value | Number |
| --- | --- |
| `FINDING_SEVERITY_UNSPECIFIED` | 0 |
| `FINDING_SEVERITY_INFO` | 1 |
| `FINDING_SEVERITY_LOW` | 2 |
| `FINDING_SEVERITY_MEDIUM` | 3 |
| `FINDING_SEVERITY_HIGH` | 4 |
| `FINDING_SEVERITY_CRITICAL` | 5 |

### FindingSource

| Value | Number |
| --- | --- |
| `FINDING_SOURCE_UNSPECIFIED` | 0 |
| `FINDING_SOURCE_DETERMINISTIC` | 1 |
| `FINDING_SOURCE_HEURISTIC` | 2 |
| `FINDING_SOURCE_MODEL` | 3 |
