---
title: Glossary
summary: One name per concept, as the pages and the code use it, with the symbol or path that defines each.
type: explanation
covers: []
covers_reason: the page defines words the other pages use and makes no claim of its own about the code
---

# Glossary

One name per concept. A page uses the name in the left column and no other
for the same thing; the right column is where the code defines it.

| Name | Meaning | Defined in |
| --- | --- | --- |
| action envelope | the structured description of one proposed action: who, on behalf of whom, what, on which resource, in which context, with which effect class; the input every decision is made from | `ActionEnvelope` in `api/proto/guardana/control/v1/action_envelope.proto`, validated by `pkg/contract` |
| action digest | the canonical digest of an envelope with its authorized arguments, `sha256:` and 64 lowercase hex digits; what a decision names, an approval binds to and the executed bytes are compared with | `canon.DigestV1` in `internal/canon/digest.go` |
| approval binding | the digest of an action digest under one policy bundle digest; what an approval is stored and compared against | `canon.ApprovalBinding`, `approval.Bind` in `internal/core/approval/` |
| bundle | a signed policy document with its reference, `bundle_id`, `version` and `digest`, and a serial inside the signed document; verified before it is trusted and installed as a snapshot | `PolicyBundle` in `api/proto/guardana/control/v1/bundle.proto`, `internal/policy/bundle/` |
| decision | what the kernel, or the enforcement point on its own behalf, concluded about one envelope: a verdict, its reason codes, the rules and obligations, the action digest and the bundle digest | `Decision` in `api/proto/guardana/control/v1/decision.proto`, made in `internal/core/decide.go` |
| effect class | the kind of consequence an action has, `READ` or one of the material classes; what materiality, the fail-closed table and the contract's required fields turn on | `EffectClass` in `common.proto`, `contract.IsMaterial` in `pkg/contract/effect.go` |
| enforcement mode | what the enforcement point does with a decision: `OBSERVE`, `APPROVE`, `ENFORCE` or `LOCKDOWN` in this build; never what is decided | `EnforcementMode` in `common.proto`, `internal/gateway/mode.go` |
| enforcement point | the process that runs one pipeline with an adapter, a sink and a policy source, and whose own record a hold, a halt and a block it minted live in; `cmd/guardana-gateway` in this build | `cmd/guardana-gateway/main.go`, wiring `Pipeline` in `internal/gateway/pipeline.go` |
| evidence trail | the chain of events under one request id, from `ACTION_PROPOSED` to the event that closes it, linked by `prev_event_id`; a request in flight or held has a prefix of one | `evidence.Builder` in `internal/evidence/event.go`, validated by `internal/evidence/chain.go` |
| execution | one run of an authorized call, named by the `execution_id` that `ACTION_STARTED` carries and the closing event repeats; a trail allows one | `Builder.Started` in `internal/evidence/event.go`, kept by `internal/gateway/trails.go` |
| external decision point | the organization's own policy engine, asked over AuthZEN about a call whose decision turns on its answer; the answer is an input a `DENY` rule reads, so it can veto and never grant, and its silence blocks in every mode that enforces | `gateway.DecisionPoint` in `internal/gateway/decisionpoint.go`, the answer as `core.External` in `internal/core/external.go`, the client in `adapters/authzen/` |
| fail-open read | a read the kernel lets run although the policy's availability left it undecided, under the operator's explicit setting and recorded as `FAIL_OPEN_READ_CONFIGURED`; never a material call | `Options.FailOpenRead` in `internal/core/kernel.go`, the table in `internal/core/failclosed.go` |
| held request | a request awaiting an approval that the enforcement point keeps in its own record, answered at once with a pending state and resumed by a retry that equals it | `heldRequest` in `internal/gateway/trails.go`, `Held` in `internal/gateway/approvals.go` |
| manifest | what the upstreams list plus the operator's classification of each definition, pinned by fingerprint; where a tool's effect class, resource type and trust zone come from | `adapters/mcp/manifest.go` |
| obligation | a condition on a call that proceeds, carried by an `ALLOW_WITH_OBLIGATIONS` or `REQUIRE_APPROVAL` rule; who applies each type, and what happens to one nobody applies, is on [the obligations reference](../reference/obligations.md) | `Obligation` in `decision.proto`, the catalogue in `internal/policy/rules/`, `internal/gateway/rewrite.go` |
| pipeline | the protocol-neutral enforcement pipeline an enforcement point runs: `Admit`, `Preview`, `Close` and `Abort`, the mode, the trail and the holds | `Pipeline` in `internal/gateway/pipeline.go` |
| principal | the party an action is done for, with its tenant, type, id and authentication strength; on a listener it comes from the operator's configuration or the authenticator, never from a client's claim | `Principal` in `action_envelope.proto`, `identity` in `adapters/mcp/translate.go` |
| quarantine | the spool's file for records a collector refused for good, appended to and never released, so the export cursor passes them without dropping them | `Reader.Quarantine` in `internal/spool/quarantine.go` |
| record | one event as the spool writes it to disk, a header and one JSON line, checksummed; the unit the spool appends, reads, acknowledges and quarantines | `internal/spool/frame.go` |
| reason code | the identifier of the kind of failure or match a decision carries, from the registry; never derived from error text, and never read to decide | `internal/policy/reasons/`, listed in [reference/reason-codes](../reference/reason-codes.md) |
| snapshot | one verified, compiled bundle, confirmed current at one time; never changed after it is made, so a call is decided against exactly one | `policy.Snapshot` in `internal/policy/policy.go`, served by `policy.Holder` |
| spool | the evidence sink that writes disk: an append-only, checksummed, bounded log an exporter drains and acknowledges | `spool.Spool` in `internal/spool/spool.go` |
| unrecorded read | a read that ran with a trail the sink did not take, under the explicit evidence setting; counted, never held and never closed | `Config.AllowReadsUnrecorded` in `internal/gateway/pipeline.go` |
| verdict | one of the five outcomes of a decision: `DENY`, `INDETERMINATE`, `REQUIRE_APPROVAL`, `ALLOW_WITH_OBLIGATIONS`, `ALLOW`; `INDETERMINATE` is never `ALLOW` | `Verdict` in `common.proto`, `internal/core/decide.go` |
