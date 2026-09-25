---
title: Documentation
summary: Every page, grouped by the question it answers, and every decision record.
type: project
covers: [docs/**]
generated: scripts/gen-index.go
---

# Documentation

Rendered from every page's own frontmatter and from the records. Rebuild it
with `make docs-gen`; an edit made here does not survive the next run.

## Get started

- [Try the demo](get-started/try-the-demo.md): Start a plane in front of two vulnerable MCP servers, answer a held call, pause a server, read the trail and the counters, and run the demo's scenarios.

## Guides

- [Let a decision point veto calls](guides/let-a-decision-point-veto.md): Point the gateway at an AuthZEN decision point, write the two rules that let it veto a scope, test what each answer does, and check the plane before it serves.
- [Run the gateway](guides/run-the-gateway.md): Start the MCP gateway in OBSERVE, read its health, classify the tools it sees, and move it to ENFORCE.
- [Watch a plane without a collector](guides/watch-a-plane-without-a-collector.md): Run the collector the gateway binary ships, point a plane's export at it, and check each request's trail in the file it writes.
- [Write and test a policy](guides/write-and-test-a-policy.md): Write an agent-policy/v1alpha1 document, lint it, prove what it decides with cases, and sign it into the bundle a plane loads.

## Concepts

- [Approvals and the held request](concepts/approvals-and-the-held-call.md): What the enforcement point holds when a call needs an approval, what a retry must equal to resume it, and the ways a hold ends.
- [Architecture](concepts/architecture.md): The system in context, its containers, the request path from a proposed action to a decision and its evidence, the dependency rule, and where it is going.
- [Enforcement modes](concepts/enforcement-modes.md): What each mode does with the kernel's decision, what it needs from an adapter, and which modes this build runs.
- [Evidence and the spool](concepts/evidence-and-the-spool.md): The trail every call leaves, the order its events may take, and how the spool keeps them on disk before an exporter sees them.
- [Glossary](concepts/glossary.md): One name per concept, as the pages and the code use it, with the symbol or path that defines each.
- [How a call is decided](concepts/how-a-call-is-decided.md): From a proposed action to one of five verdicts and the action enforced for it, including what happens when the kernel cannot decide.
- [The MCP gateway](concepts/mcp-gateway.md): How a tools/call becomes a decision, an authorized set of bytes and a trail, before the server sees it.

## Reference

- [Benchmarks](reference/benchmarks.md): What three calls on the authorization path cost on one machine, how they are measured, and what the numbers are not.
- [Command line](reference/cli.md): The two binaries, what each prints as its help, and the exit statuses they share.
- [Configuration](reference/configuration.md): Every key the gateway reads, with its environment variable, kind, default, whether it is required and the spellings it takes.
- [The approvals page](reference/console.md): What guardana-control console serves, what it prints, what it refuses and what it cannot tell you.
- [Dev mode](reference/dev.md): What guardana-gateway dev lays out, refuses, prints and stops, and how --scenario runs each scenario on a plane of its own.
- [MCP enforcement coverage](reference/mcp-coverage.md): Per revision, transport and method, what the gateway enforces today and what it does not.
- [Metrics](reference/metrics.md): Every metric a plane answers on /metrics, with its type, its label, the statistic it reads and what it counts.
- [Obligations](reference/obligations.md): The catalogue of obligation types a policy rule may name, and what the tree does with one.
- [Policy document format](reference/policy-format.md): Every member of an agent-policy/v1alpha1 document, the values each admits, the bounds, and the members of a policy test case.
- [Reason codes](reference/reason-codes.md): Every reason code a decision may carry, with its number and the verdict it usually accompanies.
- [Scenario format](reference/scenario-format.md): Every member of an agent-scenario/v1alpha1 document, how scenario run holds a live plane to one, and what it exits with.
- [action_envelope.proto](reference/wire/action_envelope.md): The messages and enums of action_envelope.proto as the compiled descriptor declares them, each field with its number, cardinality and type.
- [approval.proto](reference/wire/approval.md): The messages and enums of approval.proto as the compiled descriptor declares them, each field with its number, cardinality and type.
- [bundle.proto](reference/wire/bundle.md): The messages and enums of bundle.proto as the compiled descriptor declares them, each field with its number, cardinality and type.
- [common.proto](reference/wire/common.md): The messages and enums of common.proto as the compiled descriptor declares them, each field with its number, cardinality and type.
- [decision.proto](reference/wire/decision.md): The messages and enums of decision.proto as the compiled descriptor declares them, each field with its number, cardinality and type.
- [event.proto](reference/wire/event.md): The messages and enums of event.proto as the compiled descriptor declares them, each field with its number, cardinality and type.
- [finding.proto](reference/wire/finding.md): The messages and enums of finding.proto as the compiled descriptor declares them, each field with its number, cardinality and type.
- [result.proto](reference/wire/result.md): The messages and enums of result.proto as the compiled descriptor declares them, each field with its number, cardinality and type.

## Specification

- [Wire contracts](contracts.md): What the v1 messages are versioned by, what a receiver refuses them for, and how the evidence events chain.

## Extending

- [Writing an adapter](extending/adapters.md): The internal seam a protocol adapter implements, the capabilities it declares, and the conformance each one owes.

## Project

- [Dependencies](dependencies.md): Every direct dependency and pinned tool of the module, why the standard library will not do, its licence and whether it sits in the request path.
- [Implementation status](status.md): What exists in this repository today, component by component, the one inventory every other page points to.

## Decision records

- [ADR-0001: Go, one pinned compiler version](adr/0001-language-and-toolchain.md): accepted
- [ADR-0002: Protobuf contracts, generated code committed](adr/0002-wire-contracts-and-versioning.md): accepted
- [ADR-0003: Structured predicates, deny-overrides, external PDP over AuthZEN](adr/0003-policy-model-and-external-pdp.md): accepted
- [ADR-0004: Evidence defaults to metadata; content capture is opt-in](adr/0004-evidence-and-privacy-defaults.md): accepted
- [ADR-0005: One canonical digest identifies an action](adr/0005-canonical-action-digest.md): accepted
- [ADR-0006: The product name is a working name and can be switched](adr/0006-product-name-switch.md): accepted
- [ADR-0007: Repository layout and the dependency rule for the decision path](adr/0007-repository-layout-and-dependency-rule.md): accepted
- [ADR-0008: Protected main, DCO, semver, releases built by CI](adr/0008-branching-and-release-policy.md): accepted
- [ADR-0009: Everything in this repository is Apache-2.0 and stays open](adr/0009-open-core-boundary.md): accepted
- [ADR-0010: The digest domain tag is fixed and carries no product name](adr/0010-digest-domain-separation.md): accepted
- [ADR-0011: Corrections to the v1 contract before it is first published](adr/0011-contract-corrections-before-publication.md): accepted
- [ADR-0012: How the built-in policy kernel decides](adr/0012-policy-kernel-semantics.md): accepted
- [ADR-0013: The MCP gateway intercepts a call before it happens](adr/0013-mcp-interception-approvals-and-modes.md): accepted
- [ADR-0014: Evidence goes to a local spool first, and the spool decides what blocks](adr/0014-evidence-spool-and-sinks.md): accepted
- [ADR-0015: The documentation is a checked structure](adr/0015-documentation-structure-and-checks.md): accepted
- [ADR-0016: An approver outside the plane answers, and a lost hold is closed](adr/0016-approval-providers-and-the-lost-hold.md): accepted
- [ADR-0017: An external decision point can veto what the signed policy allows](adr/0017-an-external-decision-point-can-veto.md): accepted
- [ADR-0018: A policy key and a signed bundle each have one format on disk](adr/0018-keys-and-bundles-on-disk.md): accepted
- [ADR-0019: An operator can pause calls, and a pause the plane cannot read blocks](adr/0019-an-operator-can-pause-calls.md): accepted
- [ADR-0020: A plane's trail can land in a local file, and its counters in /metrics](adr/0020-a-trail-and-counters-without-a-collector.md): accepted
- [ADR-0021: The plane keeps what each run took in, so a flow rule can fire](adr/0021-a-run-carries-what-it-took-in.md): accepted
- [ADR-0022: A scenario is data a live plane is held to](adr/0022-scenarios-are-data.md): accepted
- [ADR-0023: A local page answers through the directory](adr/0023-a-local-page-answers-through-the-directory.md): accepted
- [ADR-0024: Guardana Control and Guardana are independent](adr/0024-control-and-guardana-are-independent.md): accepted
- [ADR-0025: The public repository: who pushes, who merges, who releases](adr/0025-public-repository-merges-and-releases.md): accepted
