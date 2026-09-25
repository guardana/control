---
title: common.proto
summary: The messages and enums of common.proto as the compiled descriptor declares them, each field with its number, cardinality and type.
type: reference
covers: [api/proto/guardana/control/v1/common.proto]
generated: scripts/gen-wire.go
---

# common.proto

What `common.proto` of package `guardana.control.v1` puts on the wire: every message with its
fields and every enum with its values, as the compiled descriptor declares them. The
descriptor carries no comments, so what a field means, when it is set and what a reader
does with a number it does not know is on [the wire contract](../../contracts.md).

Rendered from the descriptor compiled from `api/proto/guardana/control/v1/common.proto`. Rebuild it with
`make docs-gen`; an edit made here does not survive the next run.

## Enums

### EffectClass

| Value | Number |
| --- | --- |
| `EFFECT_CLASS_UNSPECIFIED` | 0 |
| `EFFECT_CLASS_READ` | 1 |
| `EFFECT_CLASS_WRITE` | 2 |
| `EFFECT_CLASS_DELETE` | 3 |
| `EFFECT_CLASS_EXECUTE` | 4 |
| `EFFECT_CLASS_COMMUNICATE` | 5 |
| `EFFECT_CLASS_TRANSACT` | 6 |
| `EFFECT_CLASS_IDENTITY_OR_ACCESS` | 7 |
| `EFFECT_CLASS_CONFIGURE` | 8 |
| `EFFECT_CLASS_SPAWN_OR_DELEGATE` | 9 |

### Sensitivity

| Value | Number |
| --- | --- |
| `SENSITIVITY_UNSPECIFIED` | 0 |
| `SENSITIVITY_PUBLIC` | 1 |
| `SENSITIVITY_INTERNAL` | 2 |
| `SENSITIVITY_CONFIDENTIAL` | 3 |
| `SENSITIVITY_RESTRICTED` | 4 |
| `SENSITIVITY_SECRET` | 5 |

### TrustZone

| Value | Number |
| --- | --- |
| `TRUST_ZONE_UNSPECIFIED` | 0 |
| `TRUST_ZONE_TRUSTED_INTERNAL` | 1 |
| `TRUST_ZONE_PARTNER` | 2 |
| `TRUST_ZONE_UNTRUSTED_EXTERNAL` | 3 |
| `TRUST_ZONE_USER_CONTROLLED` | 4 |
| `TRUST_ZONE_MODEL_GENERATED` | 5 |

### Verdict

| Value | Number |
| --- | --- |
| `VERDICT_UNSPECIFIED` | 0 |
| `VERDICT_ALLOW` | 1 |
| `VERDICT_DENY` | 2 |
| `VERDICT_REQUIRE_APPROVAL` | 3 |
| `VERDICT_ALLOW_WITH_OBLIGATIONS` | 4 |
| `VERDICT_INDETERMINATE` | 5 |

### EnforcementMode

| Value | Number |
| --- | --- |
| `ENFORCEMENT_MODE_UNSPECIFIED` | 0 |
| `ENFORCEMENT_MODE_OBSERVE` | 1 |
| `ENFORCEMENT_MODE_SHADOW` | 2 |
| `ENFORCEMENT_MODE_WARN` | 3 |
| `ENFORCEMENT_MODE_APPROVE` | 4 |
| `ENFORCEMENT_MODE_ENFORCE` | 5 |
| `ENFORCEMENT_MODE_LOCKDOWN` | 6 |

### PolicyFreshness

| Value | Number |
| --- | --- |
| `POLICY_FRESHNESS_UNSPECIFIED` | 0 |
| `POLICY_FRESHNESS_FRESH` | 1 |
| `POLICY_FRESHNESS_STALE` | 2 |
