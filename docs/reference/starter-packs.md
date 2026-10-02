---
title: Starter policy packs
summary: The read-only and approval-for-writes packs, what each decides, what it assumes about the plane, the cases that prove it, and what it leaves unguarded.
type: reference
covers: [examples/starter-packs/**, cmd/guardana-control/starterpacks_test.go]
---

# Starter policy packs

Two policy documents to start from, each with the cases that prove what it
decides. They are `experimental`: a later release may change a rule. Each
pack is a directory under
[examples/starter-packs](../../examples/starter-packs): `policy.json`, the
document, and `cases/`, one [policy test](policy-format.md#a-test-case) case
per decision it relies on, allow, deny and unknown among them. A test in
`cmd/guardana-control` runs every case and holds each case's document to the
pack's `policy.json`.

```
$ guardana-control policy test examples/starter-packs/read-only/cases
$ guardana-control policy explain examples/starter-packs/read-only/cases/unknown-unclassified.json
```

To use one, copy `policy.json`, give the bundle your own `id`, `version` and
`serial`, then lint, test and sign it as
[write-and-test-a-policy.md](../guides/write-and-test-a-policy.md) says.

## read-only

| Rule | Effect | When |
| --- | --- | --- |
| `allow-reads` | `ALLOW` | the action's effect class is `READ` |

Every other call matches no rule, and no rule matched is `DENY` with
`NO_MATCHING_RULE`.

| Case | Decision |
| --- | --- |
| `allow-read` | `ALLOW Execute [RULE_ALLOW]` |
| `deny-write`, `deny-message`, `deny-transact` | `DENY Block [NO_MATCHING_RULE]` |
| `unknown-unclassified`: the call names no effect class | `INDETERMINATE Block [REQUIRED_FIELD_ABSENT]` |
| `unknown-stale-read`: the policy is older than its budget | `INDETERMINATE Block [POLICY_STALE RULE_ALLOW]` |
| `stale-read-fail-open`: the same, with `fail_open_read` | `INDETERMINATE Execute [POLICY_STALE RULE_ALLOW FAIL_OPEN_READ_CONFIGURED]` |

Left unguarded: what a read carries out in its arguments. A read of an
untrusted page whose address holds data the agent read earlier is still a
read. The pack has no toxic-flow rule because a plane's calls carry no data
label: after the run's first untrusted result, every later call to an
untrusted destination, reads included, would be undetermined and blocked, or
denied once something sensitive was read. Add
one, as the approval-for-writes pack does for messages, if that is the
trade you want.

## approval-for-writes

| Rule | Effect | When |
| --- | --- | --- |
| `no-deletes-in-prod` | `DENY`, `ENVIRONMENT_BOUNDARY` | effect `DELETE` and `resource.environment` is `prod` |
| `no-sensitive-messages-out` | `DENY`, `TOXIC_FLOW_SENSITIVE_TO_EXTERNAL` | effect `COMMUNICATE` and the flow is toxic at `CONFIDENTIAL` |
| `allow-reads` | `ALLOW` | effect `READ` |
| `writes-need-approval` | `REQUIRE_APPROVAL` | every other effect class: `WRITE`, `DELETE`, `EXECUTE`, `COMMUNICATE`, `TRANSACT`, `IDENTITY_OR_ACCESS`, `CONFIGURE`, `SPAWN_OR_DELEGATE` |

A test holds `writes-need-approval` to every effect class the contract
declares but `READ`, so a class added later fails it until the pack names it.

| Case | Decision |
| --- | --- |
| `allow-read` | `ALLOW Execute [RULE_ALLOW]` |
| `approve-write`, `approve-delete-staging` | `REQUIRE_APPROVAL AwaitApproval [APPROVAL_REQUIRED]` |
| `deny-delete-prod` | `DENY Block [ENVIRONMENT_BOUNDARY APPROVAL_REQUIRED]` |
| `approve-delete-production`: the environment is spelled `production` | `REQUIRE_APPROVAL AwaitApproval [APPROVAL_REQUIRED]` |
| `approve-message-internal`: a message to a trusted destination after untrusted input | `REQUIRE_APPROVAL AwaitApproval [APPROVAL_REQUIRED]` |
| `deny-message-after-sensitive-read`: untrusted input, a confidential read, an untrusted destination | `DENY Block [TOXIC_FLOW_SENSITIVE_TO_EXTERNAL APPROVAL_REQUIRED]` |
| `unknown-message-after-untrusted-input`: the same with nothing confidential read | `INDETERMINATE Block [APPROVAL_REQUIRED RULE_UNDETERMINED]` |
| `unknown-write-tenant-unstated`: the resource names no tenant | `INDETERMINATE Block [TENANT_UNDETERMINED APPROVAL_REQUIRED]` |
| `unknown-unclassified` | `INDETERMINATE Block [REQUIRED_FIELD_ABSENT]` |

Two consequences to know before relying on it:

- After the run took in untrusted content, a message to an untrusted
  destination is blocked, never put to an approver: undetermined while nothing
  confidential was read, denied after. An undetermined `DENY` outranks
  `REQUIRE_APPROVAL`, and a plane's calls carry no data label to settle it.
- `prod` is matched as written. An upstream whose `environment` is
  `production` gets its deletes held for approval, not denied. Name every
  spelling your upstreams use, or one per environment.

## What both assume

- Every tool is classified: an override names its effect class. A call nothing
  classifies is blocked by the plane outside `OBSERVE`, and the kernel refuses
  an envelope without an effect class (`unknown-unclassified`).
- Each upstream states its `environment` and `tenant_id`, which the plane puts
  on the resource; the contract requires the environment of a write, a delete
  and a configuration change. A material call that names a tenant on one side
  only, the principal's or the resource's, is `INDETERMINATE`
  (`unknown-write-tenant-unstated`).
- A tool that sends a message (`COMMUNICATE`) has a `trust_zone` in its
  override: the contract requires a destination for that class.
- Each rule but the toxic-flow one reads only fields the contract requires
  for the classes it names, so no call is undetermined for want of an
  optional field. The toxic-flow rule also reads `data.sensitivities`, which a
  plane's call never carries (`unknown-message-after-untrusted-input`).

The plane's mode acts after the decision: under `OBSERVE` every case above
runs and is recorded, unless the plane blocks it for a cause of its own ([enforcement modes](../concepts/enforcement-modes.md)).
How a case is decided is [how a call is decided](../concepts/how-a-call-is-decided.md);
which rule decided it and what it lacked is
[policy-explain.md](policy-explain.md).
