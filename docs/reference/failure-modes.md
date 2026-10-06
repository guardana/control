---
title: Failure modes
summary: What a plane does when its collector, spool, decision point, an upstream, its pause file, freshness statement or clock fails, or it is killed holding calls.
type: reference
covers: [cmd/guardana-gateway/serve.go, cmd/guardana-gateway/adapter.go, cmd/guardana-gateway/chaos_*_test.go, adapters/mcp/middleware.go, adapters/mcp/answer.go, adapters/authzen/**, adapters/otel/**, internal/spool/**, internal/pause/**, internal/gateway/close.go, internal/gateway/decisionpoint.go, internal/gateway/lapse.go, internal/gateway/resume.go, internal/core/clock.go, internal/core/clock_test.go, internal/gateway/e2e/**, internal/gateway/reconcile.go, internal/policy/policy.go, internal/policywatch/**, internal/policystate/**]
---

# Failure modes

The plane is built to survive the twelve failures below. Each row says
whether the plane blocks the call, the reason code, what the agent gets,
whether the plane recovers without an operator, and the test that makes the
failure happen. The sections after the table say what the trail holds.

The tests in the `chaos_` files of `cmd/guardana-gateway/` run the built
binary as a process in `ENFORCE`, against listeners, files and a child
process that really fail. Nothing here is a security boundary
([status](../status.md)).

| Failure | The call | Reason code | The agent gets | Recovers by itself | Test |
| --- | --- | --- | --- | --- | --- |
| Collector gone, then back | runs | none | the upstream's answer | yes | `TestACollectorThatIsGoneBlocksNoCallAndGetsEveryRecordBack` |
| Spool full | blocked before its effect | `EVIDENCE_UNAVAILABLE` | a tool result with `isError: true` and the code | yes, once draining frees the budget; quarantined records count until the operator removes them | `TestAFullSpoolBlocksCallsUntilTheCollectorDrainsIt` |
| Decision point silent | blocked | `PDP_TIMEOUT` | `isError` and the code | yes, each call asks again | `TestAnAskPastTheConfiguredTimeoutIsATimeout`, `TestEveryEffectClassUnderEveryAnswer` |
| Decision point wrong or gone | blocked | `PDP_ANSWER_REFUSED`, `PDP_UNAVAILABLE` | `isError` and the code | yes, each call asks again | `TestAWrongOrAbsentDecisionPointBlocksOnARunningPlane` |
| `stdio` upstream dies mid-call | sent once, never retried | none | a JSON-RPC error | no: restart the plane | `TestAStdioUpstreamThatDiesMidCallIsNeverASuccess` |
| HTTP upstream drops the connection mid-call | sent once, never retried | none | a JSON-RPC error | yes, the next call is sent | `TestAnHTTPUpstreamThatDropsTheCallIsNeverASuccess` |
| Pause file unreadable | blocked | `PAUSE_STATE_UNAVAILABLE` | `isError` and the code | yes, at the first read of a whole file | `TestAPauseFileSpoiledWhileThePlaneRunsBlocksEveryCall` |
| Freshness statement missing, expired or refused, at start or later | blocked, a read too unless `policy.fail_open_read` | `POLICY_STALE` | `isError` and the code | yes, at the first poll that takes a statement bound to the bundle | `TestAnUnconfirmedPlaneBlocksMaterialCalls`, `TestARenewalConfirmsAgain` |
| Bundle file torn, unsigned, older or another id's | runs on the last good bundle until its statement expires, then blocked | none, then `POLICY_STALE` | the upstream's answer, then `isError` and the code | yes, once the file holds the served bundle or a newer one with its statement | `TestEveryRefusedReplacementMovesNothing` |
| Clock outside 1970 to 9999, or behind the verified time | blocked outside `OBSERVE`, a read too, whatever `policy.fail_open_read` says | `POLICY_STALE` | `isError` and the code | yes, once the clock passes the verified time, or at a restart after `policy state reset`; outside 1970 to 9999, no: fix the clock | `TestAClockOutsideTheUsableRangeIsACauseInTheRequest`, `TestAClockBehindAVerifiedTimeIsACauseInTheRequest`, `TestAClockBehindTheFloorStopsAnUnconfirmedSnapshotsRead` |
| Floor directory or file missing or unreadable at start | none: the plane does not start | none | no listener | no: restore it, or make it again with `policy state init` or `reset` | `TestAStartWithNoFloorIsRefused`, `TestAStartIsRefusedForWhatItCannotRead` |
| Plane killed with calls held | never runs | `APPROVAL_NOT_RESUMED`, `APPROVAL_REJECTED`, `APPROVAL_EXPIRED` or `APPROVAL_STATE_UNKNOWN` on the closing record | `APPROVAL_PENDING`, before the kill | at the next start, with a hold journal | `TestAnApproverOutsideThePlaneAnswersAndALostHoldIsClosed`, `TestTheLostHoldCodesTellTheFourAnswersApart` |

`APPROVAL_NOT_RESUMED`, `APPROVAL_REJECTED` and `APPROVAL_EXPIRED` are
`DENY`; every other code in the table, `APPROVAL_STATE_UNKNOWN` included, is
`INDETERMINATE` ([reason codes](reason-codes.md)). A blocked `tools/call` is
a tool result with `isError: true`; a blocked `resources/read` or
`prompts/get`, whose results cannot say `isError`, is a JSON-RPC error with
the adapter's code.

## Collector gone, then back

The exporter resends with a backoff that grows to `export.max_backoff`, and
every record stays in the spool: `/healthz` counts them in
`spool.unacknowledged` and the failed sends in `exporter.retried_transport`.
No call is blocked for it until the spool is full. When a collector answers
at the endpoint again, every record it accepts arrives. A record it refuses
again and again goes to `quarantine.log` in the spool. Delivery is at least
once, so a collector tells copies apart by `event_id`.
`TestCollectorOutageBlocksNoDecision` in `internal/gateway/e2e/` holds the
same for a collector that answers `503`.

## Spool full

If a record would take the bytes on disk and the reservations past
`evidence.max_bytes`, a call is blocked before anything runs. A read is
blocked too, unless `evidence.on_unwritable` is `allow_reads`. The block is
not recorded, because the record that would have held it is the one that
failed. A call already running writes its closing record into the room its
`ACTION_STARTED` reserved, up to the reserved size; bytes past it are
checked like any record's (`TestAClosingRecordWritesOnAFullSpool`). Calls
run again once the exporter has delivered and the spool has released what
the collector accepted.

## Decision point silent, wrong or gone

The plane asks the decision point only when its answer would change a call's
decision, so a failure blocks only calls a veto rule covers. Its trail ends
`ACTION_BLOCKED` with the code, and nothing was started. An ask past
`pdp.timeout` is `PDP_TIMEOUT`. An answer the plane cannot read as an allow
or a deny is `PDP_ANSWER_REFUSED`: two `decision` members, even when they
agree; a string for a boolean; a truncated or trailing document; an unknown
member on an allow; or no decision at all. A decision point with nothing
listening, a `500` answer, or an ask past `pdp.max_in_flight` is
`PDP_UNAVAILABLE`. A well-formed `false` is `PDP_DENY`; a forged `true` is
read as an allow ([threat model](../concepts/threat-model.md)).

## An upstream that dies mid-call

The call was decided, started and sent, and the upstream took it; no answer
came back. The agent gets a JSON-RPC error, never a result, and the plane
does not send the call again. The trail ends `ACTION_FAILED`, never
`ACTION_COMPLETED`.

- A `stdio` upstream is a child process. When it exits during a call, the
  result is `UNKNOWN` with `tool_protocol_status: error`. The plane does not
  start it again: every later call to it is decided, recorded as started,
  closed `ACTION_FAILED` with an `UNKNOWN` result and answered with an
  error, until the plane restarts. Calls to the other upstreams run.
- An HTTP upstream that closes the connection after reading the call, before
  or after its status line, is recorded `UNKNOWN` with
  `tool_protocol_status: error`: the outcome of that call is unknown. The
  next call to the same upstream is sent and answered.

An upstream that answers nothing within `upstream.call_timeout` is closed
with a `TIMEOUT` result (`TestUpstreamTimeoutIsRecordedAsOne`).

## Pause file unreadable

At start, a pause file that is missing, garbled, too large, a link, a
directory, or writable by the group or others stops the plane before it
listens, naming `pause.file` and the cause
(`TestAStartIsRefusedOnEveryUnknownCause`). While the plane runs, the reader
takes the file once every `pause.poll_interval`. If a read meets one of
those causes, or the file cannot be read, every call is blocked until a read
finds a whole file again. A read older than three intervals is unknown as
well. `/healthz` answers `503` with `pause.state: unknown` and the cause
(`TestHealthAnswers503OnEveryUnknownState`), and the trail of each call ends
`ACTION_BLOCKED` with the code.

## A policy that is not confirmed

Every poll rereads the bundle and the statement. A refusal is
logged and counted under its cause in
`guardana_control_policy_refresh_refused_total`: `bundle_unreadable`,
`bundle_invalid`, `bundle_id`, `bundle_budget`, `rollback`, `serial_reused`,
`statement_missing`, `statement_unreadable`, `statement_invalid`,
`statement_unbound`, `statement_future`, `statement_expired`, `below_floor`,
`clock_behind_floor`, `clock_back`, `withdrawn`, `floor` or `unknown`. The
bundle in use keeps its confirmation. A refused bundle file stops the renewals
of the bundle in use too, since each poll pairs the statement with the file:
the plane turns stale when the last statement's budget ends. While
the policy is not confirmed, each call's `POLICY_DECIDED` carries
`POLICY_STALE` and `policy_freshness` `STALE`, `/healthz` answers
`"status":"degraded"`, and outside `OBSERVE` a call is blocked with
`ACTION_BLOCKED`, a read too unless `policy.fail_open_read`.

## A clock that cannot be read as now

The kernel judges no expiry and no bundle's age by a reading before 1970,
after 9999-12-31, or behind the
[verified time](../concepts/glossary.md). It decides
`INDETERMINATE` with `POLICY_STALE` alone, and `policy.fail_open_read` opens
no read: a wrong clock is no missing policy. A hop's unsigned `issued_at`
is not compared.

Outside 1970 to 9999 the kernel's decision has no `decided_at`, though the
plane's records carry the reading. The spool refuses a record dated before
year 1 or after 9999, which blocks the call with `EVIDENCE_UNAVAILABLE`
unless `evidence.on_unwritable: allow_reads` lets a read go on unrecorded.

Before handing out an approved or run-bound call, a reading outside 1970
to 9999, or behind one the call took before, the verified time or the
approval's `requested_at`, blocks it as lapsed: `APPROVAL_EXPIRED` or `EVIDENCE_UNAVAILABLE`.

## Plane killed with calls held

A held call's trail stands at `APPROVAL_REQUESTED` while no plane runs, and
an approver can still answer its record. With `approvals.hold_journal_dir`
set, the next `run` closes that trail. It records the answer, then
`ACTION_BLOCKED` with `APPROVAL_NOT_RESUMED` for an approval,
`APPROVAL_REJECTED` for a rejection, or `APPROVAL_EXPIRED` when nobody
answered or the approval expired. When the store cannot be read, has lost a
record whose approval has not expired, or answers with a record that fails
its checks, an approval with no approver or a state no answer has, the block
is `INDETERMINATE` with `APPROVAL_STATE_UNKNOWN`: the plane does not know what
the approver said. A crash between journalling a hold and filing its record,
followed by a restart before the expiry, closes that way too, because the
plane cannot tell a record never filed from one deleted. The call never runs,
and `run` prints `lost holds:` with the count it closed. The reconciliation is
bounded, and what it cannot settle stays open: a journal it cannot read, or
more entries than its bound, sets
`guardana_control_pipeline_reconcile_incomplete` to 1, and an entry it
cannot settle counts in `guardana_control_pipeline_holds_unmeasured_total`.
Without a hold journal the trail is never closed, and `/healthz` says so
under `approvals.limits`.
