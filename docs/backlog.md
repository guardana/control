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

This backlog covers roadmap work, not every session note. Keep stable task identifiers when work moves to an
issue. Completion requires code, documentation and the applicable checks.

## Entry gate and trust work

| ID | Work | Depends on | Acceptance |
| --- | --- | --- | --- |
| B00 | Establish a reproducible baseline | none | Full `make quality` and CI on the exact candidate commit pass; platform, tool versions, skips and failures are recorded. A partial target run cannot satisfy this task |

## Milestone 1: one useful local project

| ID | Work | Depends on | Acceptance |
| --- | --- | --- | --- |
| B06 | Connect an existing MCP setup | none | Copyable client/server instructions and a local profile work with an independently maintained server. Classification remains operator-owned; missing classification and unsupported methods are visible. At least three new users attempt the guide; time and help are recorded |

## Milestone 2: one task under supervision, across two channels

[ADR-0039](adr/0039-many-channels-into-one-core.md) sets the vocabulary: an
enforcement point decides before the effect, a sensor reports after it, a
detector raises findings over exports, a reaction stops within a scope, a
notifier delivers. Observations stay apart from enforcement evidence, and every
view says what it did not see.

| ID | Work | Depends on | Acceptance |
| --- | --- | --- | --- |
| B18 | Map coverage per declared path | ADR-0040 | A command reads the planes' manifests, the source descriptors and an operator's inventory of paths, and prints each path enforced, decided but not enforced, observed with its source's trust, inferred or not covered, plus a row for undeclared paths. Tests: a plane in `OBSERVE` never shows a path enforced; a path only a self-reported source saw says so; a silent source turns its paths unknown; a removed descriptor turns its paths not covered |
| B15 | Add procedure supervision over one run | ADR-0040 | The demonstration run is a shop refund: fetch the order, assess the refund, an approval, the refund; its deviation looks up other customers' orders (denied four times) and sends data to a host the plane never saw, which only a trace or a proxy log shows. Versioned procedures bind to runs; reports cover out-of-order and skipped required steps, failed-step continuation, repeated denial and deadline, over both the evidence and the observation exports. Findings go to a findings log with typed references to events and observations; a finding resting only on self-reported observations is at most suspected; a gap or a duplicate is unknown. One local notifier delivers each finding at least once, keyed on its id, and the state resumes after a crash. The run's view answers what was meant, what happened, where a boundary was crossed, what was stopped and what was not seen; it marks a step done as planned, an exception or approval, a block and missing data by shape and line as well as colour, works without motion, and shows one run and the neighbourhood of a deviation by default. Findings never grant authority, add no latency to a decision and change no verdict |
| B19 | Stop one run from a finding | B15, ADR on the reaction source | A reaction source, apart from the operator's pause file and add-only for the emitter, holds entries with a run scope, an expiry and the finding that caused each. A plane with runs refuses that run's later calls and no other run's; a plane that cannot serve a scope a route names refuses to start. Only a deterministic, confirmed finding stops by default; a repeated finding writes one entry; a call already running is not cut, and the docs say so. A route is verified under a key apart from the policy key, and its serial never goes down. An unreadable or unverifiable reaction source blocks every call on the planes that read it, as an unreadable pause file does |

The existing event wire format is reused where it suffices. A query response
or delivery wrapper has its own version; do not quietly add unknown fields to
a strict v1 decoder. OTLP logs and traces need separate, tested destination
mappings.

## Milestone 3: integrations without a fork

| ID | Work | Depends on | Acceptance |
| --- | --- | --- | --- |
| B10 | Specify and build the enforcement API | B00 | ADR defines admit/complete/abort, scoped opaque handles, expiry, authorized bytes, pending/resume, duplicate requests and abandoned executions. Authenticated scope is receiver-owned. Timeout after a possible effect produces an uncertain result and no automatic retry. A test distinguishes a cooperating client from an enforcement boundary |
| B11 | Ship Python and one framework integration | B10 | A custom-tool wrapper and one adopter-selected framework integration run externally. Conformance covers block-before-send, rewritten bytes, pending approval, concurrent resume, unknown obligation, timeout, cancellation and completion. Publish covered and bypassing tool paths; digest parity precedes client-side hashes |
| B21 | Add sensors independent of the agent | ADR-0040 | An importer reads an egress proxy's access log under a descriptor marked independent, and a second reads process events from the host the agent runs on; a connection is attributed to a run only as well as the proxy identifies its caller, and an unattributed one stays unknown. The coverage map shows an egress destination the plane never decided on |
| B12 | Publish the port contracts and add TypeScript | B15; B11 for the enforcement client | Sensor, detector, reaction and notifier contracts are published with conformance suites of input and expected output files any language runs, once two consumers have used each. An external detector or notifier built from them alone passes its suite. Queue bounds, lag and failures are visible and cannot affect authorization. TypeScript passes the same client fixtures. A Go seam is promoted only with its own ADR and two consumers; nothing is loaded into a plane at run time |

LangGraph and the OpenAI Agents SDK are candidates, not simultaneous delivery
requirements. Select the first framework integration with adopters and test the framework's
resume behavior. Transport retries may not replay a material tool execution.
Custom policy-provider work preserves ADR-0017's veto-only semantics.

## Milestone 4: a small team

| ID | Work | Depends on | Acceptance |
| --- | --- | --- | --- |
| B13 | Authenticate operators and scope authority | B10 | Reader/operator/admin capabilities are receiver-enforced; approval authority comes from authentication. Cross-project reads and operations are refused; a pause or a stop names its actual run, agent or principal scope, and only an authenticated author may sign a reaction route. Two users and overlapping runs pass adversarial tests. MCP upstream credentials remain separate from inbound credentials |
| B14 | Add retention and recovery | none | Rotation, disk budget, cursor expiry, duplicates, interrupted export and backup/restore are tested for evidence and observations. A restored process cannot reuse spent approval authority or forget its rollback floor. Read-only inspection cannot mutate recovery state without saying so |

## Milestone 5: a fleet and wider protocols

| ID | Work | Depends on | Acceptance |
| --- | --- | --- | --- |
| B16 | Add fleet operations and protocol breadth from demand | B13, B14, B15 | Independent deployments demonstrate need; design covers tenant isolation, signed distribution, authenticated APIs and tested recovery. New HTTP/A2A adapters pass conformance. HA and additional stores have reproducible benchmarks before scale claims |

## Adoption checkpoints

Targets are hypotheses, not reported achievements: first demo value in ten
minutes; three new users completing onboarding; two custom evidence consumers;
one external framework integration; one small team running overlapping tasks. Measure,
each with its denominator and test setup: the time from a release download to
a user's first protected action of their own; the share of declared paths
enforced, observed and unknown; the time from a deviation to its finding, to
its alert's delivery and to the next action stopped; false alarms per run; the
share of runs with complete evidence; the hours an outside author needs to
build a sensor that passes its suite. Record failures and assistance as well
as time. Prioritize observed blockers before adding frameworks or a detector
catalogue. Do not collect adoption telemetry from installations: use voluntary
sessions and feedback, consistent with the no-phone-home decision.
