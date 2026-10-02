---
title: Policy explain
summary: What `guardana-control policy explain` reads, every line it prints, which rule decided and which input a rule could not read, and its bounds.
type: reference
covers: [cmd/guardana-control/explain.go, internal/core/explain.go, internal/policy/match/explain.go]
---

# Policy explain

`guardana-control policy explain <case>` decides one case the way `policy test`
does and prints how: the decision, what the kernel found before the policy,
and how each rule read the call. It is `implemented`. Why it reads the kernel's
own evaluation, and nothing beside it, is
[ADR-0036](../adr/0036-policy-explain-reads-the-kernels-own-evaluation.md).

## What it reads

One case file in the format of
[policy-format.md](policy-format.md#a-test-case), with `expect` optional. The
document is signed in memory, loaded at `loaded_at` and decided at
`decided_at`, exactly as `policy test` decides it, and the decision printed is
the one `policy test` compares. When the case has `expect`, the last line says
whether the decision met it.

```
$ guardana-control policy explain examples/starter-packs/approval-for-writes/cases/unknown-message-after-untrusted-input.json
verdict: INDETERMINATE
kernel_action: Block
enforcement_mode: ENFORCE
bundle: "starter-approval-for-writes" "1"
policy_digest: sha256:<64 hex digits>
freshness: FRESH
reason_codes: APPROVAL_REQUIRED RULE_UNDETERMINED
refused: no
tenant_unstated: none
delegation: absent
external: not asked
rules: 4 read; 1 matched, 1 undetermined, 0 allow undetermined, 2 not matched
rule "writes-need-approval" REQUIRE_APPROVAL matched
rule "no-sensitive-messages-out" DENY undetermined: when.flow.toxicAtLeast needs data.sensitivities
rule "no-deletes-in-prod" DENY not matched: when.action.effect
rule "allow-reads" ALLOW not matched: when.action.effect
expect: met
```

The message would be held for approval, but one rule could not be told: the
run took in untrusted content, the destination is untrusted, and nothing says
how sensitive the message is. An undetermined `DENY` outranks the approval, so
the call is blocked before anyone is asked.

## The lines

| Line | Says |
| --- | --- |
| `verdict` | The decision's verdict. |
| `kernel_action` | What the kernel asks the enforcement point to do: `Block`, `Execute`, `ExecuteWithObligations` or `AwaitApproval`. |
| `enforcement_mode` | The decision's own mode, always `ENFORCE`. A plane's mode acts on the decision after it is made, and this command does not know it: under `OBSERVE` a blocked call runs and is recorded ([enforcement modes](../concepts/enforcement-modes.md)). |
| `bundle` | The bundle's id and version, quoted. |
| `policy_digest` | The digest the decision names. |
| `freshness` | `FRESH`, or `STALE` when the policy is older than the smaller of the document's and the options' budgets, or was loaded after the decision time. |
| `reason_codes` | The decision's codes, in order ([reason codes](reason-codes.md)). |
| `refused` | `no`; the envelope field whose refusal stopped the decision before the policy; or `yes, naming no field` when the refusal is about the message as a whole or its arguments, which the codes tell apart. The refusal's own message is never printed: it can quote the input. |
| `tenant_unstated` | `none`, or the tenant field a material call left out while the other side named one (`TENANT_UNDETERMINED`). |
| `delegation` | `absent`, `passed`, `refused` with the code, or `not checked` when the request was refused first. |
| `external` | The decision point's answer as the case names it, or `not asked`. |
| `rules` | How many rules were read and how they fell, or why none was read. |
| `rule` | One line per rule, below. |
| `rules_not_listed` | How many rules the bound left out. |
| `expect` | `met`, or `not met, want ...` in the spelling of `policy test`. Only when the case has `expect`. |

## A rule's line

The rules are listed in four groups, those that decided first, each group in
document order:

| Line | The rule |
| --- | --- |
| `rule "<id>" <EFFECT> matched` | Every constraint held. |
| `rule "<id>" <EFFECT> undetermined: <field> needs <input>; ...` | A `DENY`, `REQUIRE_APPROVAL` or `ALLOW_WITH_OBLIGATIONS` rule no constraint failed and one could not read: the decision is `INDETERMINATE` unless a `DENY` decides it. |
| `rule "<id>" ALLOW undetermined, no effect: <field> needs <input>` | An `ALLOW` rule left undetermined, which is no match: it allows nothing. |
| `rule "<id>" <EFFECT> not matched: <field>; ...` | The constraints that did not hold. |

`<field>` is the document's spelling, `when.resource.environment`; `<input>` is
what the call or the run lacked, in the envelope's wire spelling:

| Constraint | Needs |
| --- | --- |
| a field of `principal`, `agent`, `action`, `resource` or `destination` | that field, `resource.environment`; a map key as `resource.labels["tier"]` |
| `data.sensitivityAtLeast` | `data.sensitivities` |
| `flow.toxicAtLeast` | `flow` when the run was not tracked; else `flow.max_sensitivity_read`, `data.sensitivities` or both, whichever is unknown |
| `delegation.scopes` | `delegation`: the call carried no chain, or the chain was refused (then the decision is already `DENY`) |
| `external.denies` | `external`: the decision point gave no answer |

An enum this build does not declare, or a sensitivity off the scale, is
refused before any rule is read, and no rule line says it was missing.

## Bounds

At most 32 rule lines, the rest counted on `rules_not_listed`, and at most 8
constraints on one line, then `and <n> more`. Every line is one line: a control
character makes it a quoted Go string, and key text is withheld
([cli.md](cli.md)).

## Exit status

| Status | Means |
| --- | --- |
| 0 | Explained; the decision met `expect`, or the case has none. |
| 1 | The case could not be read or decided (one line on stderr), or the decision did not meet `expect` (the whole explanation, then `expect: not met`). |
| 2 | Usage: no case, or more than one. |

The output carries no version and may change between releases. A script reads
`policy test`, whose line is stable.
