---
title: MCP enforcement coverage
summary: Per revision, transport and method, what the gateway enforces today and what it does not.
type: reference
covers: [adapters/mcp/**, internal/gateway/**, cmd/guardana-gateway/**, pkg/contract/effect.go]
---

# MCP enforcement coverage

What this build of the gateway does with each Model Context Protocol method, on
each revision and transport. [status.md](../status.md) is the inventory;
[concepts/mcp-gateway](../concepts/mcp-gateway.md) explains the mechanism, and
[ADR-0013](../adr/0013-mcp-interception-approvals-and-modes.md) records the
decisions.

## Revisions and transports

| Listener | Serves | Identity per call comes from | Notes |
| --- | --- | --- | --- |
| `stateless_http` | `2026-07-28` | the listener's configuration | No session; every request carries its own protocol version and client claim in `_meta` |
| `stateful_http` | `2025-11-25` and older | the listener's configuration | A `2026-07-28` request is answered `-32022` with the versions the listener serves, so a client renegotiates |
| `stdio` | one agent over a pipe | the listener's configuration | Nothing authenticates a pipe |

An end-user identity taken from a request's credential is not in this build: the
adapter takes an authenticator that establishes one, and no configuration key
wires it, so every call on a listener is made by the principal the operator
configured. `_meta.clientInfo` is recorded as a run-context tag and never reaches
a field the digest covers.

Upstream servers are reached over Streamable HTTP (`upstreams[].endpoint`) or as
a child process over its standard input and output (`upstreams[].command`).

## Methods

| Method | What the gateway does | Decided |
| --- | --- | --- |
| `tools/list` | Answers from the manifest, shaped for the principal, with `cacheScope: private` and the operator's `ttlMs` | on an uncached `annotate` or `hide` list, one preview per classified tool one upstream serves, recording nothing |
| `tools/call` | Translates to an `ActionEnvelope`, admits it, applies rewriting obligations, sends exactly the authorized bytes, closes the trail with the digest of what was sent | yes |
| `resources/read` | Translated as a `READ` of the URI. Routed only when one upstream is configured; with several the call is `ACTION_UNCLASSIFIED`, since nothing says which server holds the URI | yes |
| `prompts/get` | Translated as a `READ` of the prompt, its arguments authorized as a canonical JSON object of strings. Routed as above | yes |
| `resources/list`, `resources/templates/list`, `prompts/list` | Merged from every upstream, bounded, with the gateway's own `_meta` keys stripped and `cacheScope: private` | no: a listing names no action |
| everything else | Handled by the library's own server, which has no tool, resource or prompt registered, so nothing is forwarded | no |

## What the gateway cannot do

| Not covered | Why |
| --- | --- |
| Elicitation (`input_required`) | The shape needs a handler registered per tool, which this interception does not have; `planned` with the approval providers (ADR-0013) |
| The Tasks extension | Not implemented by the library. A `resultType: "task"` result was seen to decode as a complete, empty, successful call when this was measured against the library; no test in this repository pins that, so it is a reason not to use the extension rather than a promise about it |
| `notifications/tools/list_changed` toward the agent | The library emits it only for its own registry, and the gateway registers no tools, so the listener declares it does not send one and an agent refreshes when `list.ttl` runs out |
| An upstream's own notifications and server-initiated requests, other than a changed tool list | Only a changed tool list is handled, by refreshing the manifest; sampling, roots and progress from an upstream reach no agent |
| Resuming a hold the plane lost | A hold does not survive a restart. A plane that keeps a hold journal closes that trail when it comes back, with the approver's answer and `APPROVAL_NOT_RESUMED`, or as expired where nobody answered, and the call is never run; one configured without a journal leaves it standing. The agent asks again and an approver answers again (ADR-0016) |
| Telling one approver from another | Write access to the approvals directory is the approval authority, and `approver_id` on a record is a claim recorded as given. An authenticated provider would replace that authority, and none exists yet (ADR-0016) |
| A client other than the library's own | Not exercised; the conformance tests drive the reference implementation of both revisions |
| A tool classified `SPAWN_OR_DELEGATE` | The contract requires a delegation chain for that class and the listener carries none, so every such call is refused as `REQUIRED_FIELD_ABSENT` before a rule or a decision point is read |
| What a run read outside the gateway | A run is the listener's principal and starts clean at each restart; on stdio the gateway ends with its client's connection, so a run lasts one connection there. The user's prompt, tool descriptions and tools not behind the gateway are not tracked, and a call carries no data label, so a `flow` rule blocks every untrusted destination after untrusted input. A `resources/read` and a `prompts/get` carry no declared result, so each leaves its run untrusted and unknown for the rest of the run (ADR-0021) |

## List shaping under each mode

`list.shaping` only ever subtracts, and the configuration is refused at start
when it is not `none` in a mode that does not enforce.

| Shaping | `OBSERVE` | `APPROVE`, `ENFORCE`, `LOCKDOWN` |
| --- | --- | --- |
| `none` | the upstream's list, `_meta` of the gateway's namespace stripped | the same |
| `annotate` | refused at start | each tool carries the verdict a call to it would get, under the gateway's `_meta` key |
| `hide` | refused at start | a tool the preview denies, and every unclassified tool, is omitted |

A preview is one decision made with no arguments, so a tool whose
`resource_from` reads the resource out of the arguments cannot be decided before
the call exists. Such a tool stays in the list, and under `annotate` it is marked
`DECIDED_PER_CALL` rather than allowed or denied. Under `hide` it stays too:
shaping subtracts what policy denies, never what it could not decide.

## The two codes the adapter mints

A `tools/call` answers a block and a pending state as a tool result with
`isError: true`, which the model reads. A `resources/read` and a `prompts/get`
have no such field, so there the same structured data travels as a JSON-RPC
error, outside the range the protocol reserves and away from the code the library
uses privately.

| Code | Means | Data |
| --- | --- | --- |
| `-31100` | the gateway blocked the call | `reason_codes`, `decision_id`, and `refused` with what the refusal said when the envelope itself could not be built |
| `-31101` | an approval is pending | `reason_code: APPROVAL_PENDING`, `approval_id`, `action_digest`, `expires_at`, `retry_after` |

An upstream's own wire error passes through with its own code, and to a
`tools/call` with its own message and data. Only `<ns>/answer` (`blocked` or
`pending`, `<ns>` being the product's namespace) in an answer's `_meta` or in
the error's `data` marks an answer the gateway made. The namespace is stripped
from the same two places in everything an upstream sends, a result's `_meta`
and the top level of a wire error's `data`, so an upstream cannot answer as the
gateway.

A `tools/call` answer the pipeline decided names its decision's trail with the
strings `<ns>/request_id` and `<ns>/decision_id`:

| Answer | Where the ids travel |
| --- | --- |
| a block, a pending state, the upstream's result | the result's `_meta` |
| an upstream's wire error, `data` an object or absent | the top level of `data` |
| an upstream's wire error, other `data` | nowhere; the data arrives as sent |

A retry told to wait again, or run, names the held request's trail; one refused
on its own trail names that. The ids are added after the result is hashed,
so the closing record's `result_hash` is the upstream's. Reads, prompts, lists
and undecided answers carry neither.

## Reason codes in the gateway's answers

A decision the kernel made comes back as the kernel wrote it, so `reason_codes`
carries whatever codes the kernel emits; [reference/reason-codes](reason-codes.md)
has every code and its number. The codes below are the ones the enforcement
point, the pipeline or this adapter, mints or answers on its own.

| Code | When |
| --- | --- |
| `ACTION_UNCLASSIFIED` | no manifest entry classifies the operation; blocked outside `OBSERVE` |
| `LOCKDOWN` | a material call under `LOCKDOWN` |
| `PAUSED` | an operator's pause covers the call: `global`, its upstream, or its kind, upstream and name; in every mode, reads included, and never in a listing |
| `PAUSE_STATE_UNAVAILABLE` | the plane has a pause file and cannot read it, or its last read is older than three poll intervals or dated ahead; every call is blocked until a read succeeds |
| `EVIDENCE_UNAVAILABLE` | a record the decision depends on could not be written, kept or read back: the sink refused an event, a bound on held requests or open executions was reached, or the approval store or the hold journal could not answer, or answered with an approval that names another request, is not approved, or outlives the expiry the gateway minted |
| `APPROVAL_PENDING`, `APPROVAL_EXPIRED`, `APPROVAL_REJECTED`, `APPROVAL_ALREADY_USED` | the state of the approval this call waits on |
| `APPROVAL_NOT_RESUMED` | an approver granted a request this plane lost to a restart; the trail is closed and the call was never run |
| `APPROVAL_DIGEST_MISMATCH`, `APPROVAL_BUNDLE_MISMATCH` | an approval a store returned named another action or another policy bundle |
| `EXECUTED_ARGS_MISMATCH` | the bytes about to be sent, or the bytes sent, were not the authorized ones |
| `OBLIGATION_NOT_UNDERSTOOD` | an obligation the plane cannot apply, or whose parameters it cannot read |
| `INVALID_FIELD_VALUE`, `MALFORMED_INPUT` | the call could not be translated into a valid envelope; `INVALID_FIELD_VALUE` also when its request id names a trail still open in the gateway |
| `POLICY_UNAVAILABLE` | a block reached the adapter with a decision naming no cause; it answers with the kernel's own code for an absent policy rather than invent a second fail-closed answer |
| `REQUIRED_FIELD_ABSENT` | a `tools/list` on a listener whose authenticator established no end user |
