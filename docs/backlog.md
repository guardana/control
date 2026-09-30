---
title: Delivery backlog
summary: Planned work ordered by user value, with dependencies and observable acceptance checks.
type: project
covers: [pkg/**, cmd/**, adapters/**, internal/**, examples/**]
---

# Delivery backlog

All tasks below are `planned`, including verification tasks. They name work to
do, not capabilities to claim. [status.md](status.md) is the implementation
inventory; [ROADMAP.md](../ROADMAP.md) owns milestone order. Architectural changes
need accepted records before implementation. An owner and an issue can be added
when a task is picked up; no task is assigned by this document. A delivered
task leaves this page, and status.md records what it built.

The private session backlog contains historical findings, some already fixed.
Reproduce one in the current code before calling it a defect.
This backlog covers roadmap work, not every session note. Keep stable task identifiers when work moves to an
issue. Completion requires code, documentation and the applicable checks.

## Entry gate and trust work

| ID | Work | Depends on | Acceptance |
| --- | --- | --- | --- |
| B00 | Establish a reproducible baseline | none | Full `make quality` and CI on the exact candidate commit pass; platform, tool versions, skips and failures are recorded. A partial target run cannot satisfy this task |
| B01 | Reconcile stale planning and public claims | none | Every promoted defect has current code evidence; planned Go packages are distinguished from available ones; only the public roadmap schedules delivery. |
| B03 | Make policy freshness usable and restart-safe | B00 | Accepted ADR and tests cover atomic refresh, invalid replacement, last-known-good bounds, signed expiry, persisted serial floor and clock rollback. Unavailable policy still fails closed; rereading a file alone does not prove it is the latest policy |

B03 runs before shared use, independently of the usability work. Do not
wait for a fleet to address it.

## Milestone 1: one useful local project

| ID | Work | Depends on | Acceptance |
| --- | --- | --- | --- |
| B06 | Connect an existing MCP setup | none | Copyable client/server instructions and a local profile work with an independently maintained server. Classification remains operator-owned; missing classification and unsupported methods are visible. At least three new users attempt the guide; time and help are recorded |
| B07 | Explain decisions and ship tested starter packs | B00 | `policy explain` exposes matched rule ids, missing inputs, mode and policy digest with bounded output; read-only and approval-for-writes packs include allow/deny/unknown scenarios. Their assumptions and coverage are documented |

## Milestone 2: runs and evidence other tools can read

| ID | Work | Depends on | Acceptance |
| --- | --- | --- | --- |
| B02 | Define identity and run ownership | B01 | Accepted ADR covers authenticated versus local identity, two simultaneous runs, parent context, reconnect and restart. Forged ids and reconnects cannot erase flow state; the current ADR-0021 semantics change only through that record |
| B09 | Build a custom report and alert example | none | A consumer outside this module reports actions and reasons and emits a local alert. Fixtures cover duplicate delivery, gaps and consumer outage. It requires no extra service and records no raw payload by default |

The existing event wire format is reused where sufficient. A query response or
delivery wrapper has its own version; do not quietly add unknown fields to a
strict v1 decoder. Observed framework telemetry and plane-recorded enforcement
evidence must remain distinguishable. OTLP logs and traces need separate,
tested destination mappings.

## Milestone 3: procedures and a supervisor

| ID | Work | Depends on | Acceptance |
| --- | --- | --- | --- |
| B15 | Add procedure supervision over one run | B02 | Versioned procedures bind to runs; initial reports cover out-of-order and skipped required steps, failed-step continuation, repeated denial and deadline. The expected/observed view cites evidence and marks gaps unknown. Findings never grant authority, add no latency to a decision and change no verdict; an automated stop requires explicit authorization |

## Milestone 4: integrations without a fork

| ID | Work | Depends on | Acceptance |
| --- | --- | --- | --- |
| B10 | Specify and build the enforcement API | B00, B02 | ADR defines admit/complete/abort, scoped opaque handles, expiry, authorized bytes, pending/resume, duplicate requests and abandoned executions. Authenticated scope is receiver-owned. Timeout after a possible effect produces an uncertain result and no automatic retry. A test distinguishes a cooperating client from an enforcement boundary |
| B11 | Ship Python and one framework port | B10 | A custom-tool wrapper and one adopter-selected framework integration run externally. Conformance covers block-before-send, rewritten bytes, pending approval, concurrent resume, unknown obligation, timeout, cancellation and completion. Publish covered and bypassing tool paths; digest parity precedes client-side hashes |
| B12 | Expose asynchronous extensions and add TypeScript | B09; B11 for enforcement client | External detector/alert example has stable id/version, evidence refs and confirmed/suspected/unknown cases. Queue bounds, lag and failures are visible and cannot affect authorization. TypeScript passes the same client fixtures. Promote a Go seam only with its own ADR and two consumers; no runtime plugin loading |

LangGraph and the OpenAI Agents SDK are candidates, not simultaneous delivery
requirements. Select the first port with adopters and test the framework's
resume behavior. Transport retries may not replay a material tool execution.
Custom policy-provider work preserves ADR-0017's veto-only semantics.

## Milestone 5: a small team

| ID | Work | Depends on | Acceptance |
| --- | --- | --- | --- |
| B13 | Authenticate operators and scope authority | B02, B03, B10 | Reader/operator/admin capabilities are receiver-enforced; approval authority comes from authentication. Cross-project reads and operations are refused; a pause names its actual run/agent/principal scope. Two users and overlapping runs pass adversarial tests. MCP upstream credentials remain separate from inbound credentials |
| B14 | Add retention and recovery | none | Rotation, disk budget, cursor expiry, duplicates, interrupted export and backup/restore are tested. A restored process cannot reuse spent approval authority or forget its rollback floor. Read-only inspection cannot mutate recovery state without saying so |

## Milestone 6: a fleet and wider protocols

| ID | Work | Depends on | Acceptance |
| --- | --- | --- | --- |
| B16 | Add fleet operations and protocol breadth from demand | B13, B14, B15 | Independent deployments demonstrate need; design covers tenant isolation, signed distribution, authenticated APIs and tested recovery. New HTTP/A2A adapters pass conformance. HA and additional stores have reproducible benchmarks before scale claims |

## Adoption checkpoints

Targets are hypotheses, not reported achievements: first demo value in ten
minutes; three new users completing onboarding; two custom evidence consumers;
one external framework port; one small team running overlapping tasks. Record
failures and assistance as well as time. Prioritize observed blockers before
adding frameworks or a detector catalogue. Do not collect adoption telemetry
from installations: use voluntary sessions and feedback, consistent with the
no-phone-home decision.
