# Roadmap

What has to become true, in the order it is planned, without dates.

Guardana Control is meant to supervise an organization's AI agents. It shows
what they do and what data they reach, checks them against policy and their
procedures, reports deviations, access attempts and unfinished work, and lets
an operator with the right permission stop an agent. It gets there from one
project a person can use today: first value on one machine, then runs and
evidence other tools can read, then the supervisor, then integrations, a team
and a fleet.

Everything here is `planned`, and a finished item is deleted.
[docs/status.md](docs/status.md) is the inventory of what exists, the
[delivery backlog](docs/backlog.md) gives each step its tasks and acceptance
checks, and [ADR-0026](docs/adr/0026-first-value-and-external-extension-paths.md)
records the order.

## 1. First value in one project

Done when a new user sees an allowed call, a blocked call with its reason and
one exact-action approval within ten minutes of downloading the release's demo
archive. That is a target to measure, not a result already measured.

A guide connects an existing MCP client and server, states what it covers and
the identity it assumes, and ends in a local report. Starter policy
packs come with scenarios, and `policy explain` names the rules that matched
and the inputs that were missing. No setup step widens authority silently.

## 2. Runs and evidence other tools can read

Done when a run has an identity of its own, so two tasks of one agent never
share flow state and a new client session cannot reset it, and when an example
in another repository builds a report and an alert from the evidence export,
without importing `internal/` or parsing terminal output. Content capture stays
off.

## 3. Procedures and a supervisor

Done when a procedure is a versioned document a run names: its steps, their
order, the steps without which it is not finished, the exceptions it allows
and its deadline. A supervisor beside the agents compares each run with it and
reports steps outside the procedure or out of order, skipped required steps,
runs unfinished past their deadline, runs that carry on after a failed step,
and attempts to use a tool, resource or permission the run does not hold,
including a denied action retried in another form.

Reports cite their evidence and reach the operator's webhooks, OpenTelemetry,
metrics or SIEM. Missing evidence is reported as unknown, never as a pass, and
the supervisor adds no latency to a decision and changes no verdict. One run
shows expected and observed steps as a graph. An operator with the right
permission can stop one run, agent or principal; a severe report stops a run
only where the operator configured that.

## 4. Integrations without a fork

Done when a language-neutral enforcement API and a Python client wrap a custom
tool with admission, authorized arguments, completion and abort. They bind
identity and run, consume an approval once and never retry an uncertain
effect. One framework port chosen with early adopters ships first, then
TypeScript and a second port; each passes one conformance suite and states the
tool paths it covers. Detectors and alerts run outside authorization, over the
evidence export. A Go seam becomes public only after two consumers use it and
its own record is accepted.

## 5. A small team

Done when authenticated principals hold scoped rights to read, approve and
pause; policy refreshes atomically, with a signed expiry and a serial that
survives a restart; and evidence can be retained, rotated, exported and
recovered, none of it depending on a managed service. Cross-project access,
forged identity, approval replay and policy outage have adversarial tests.

## 6. A fleet and wider protocols, when adopters need them

Done when many planes report to one self-hosted control plane that holds the
inventory of agents, tools and procedures, distributes signed policy and keeps
tenants apart, and when HTTP tool APIs and agent-to-agent handoffs have
adapters that keep authority from growing across a handoff. Outcomes are
judged finished, degraded or unknown, with the cost per accepted outcome, and
no quality judgment influences a verdict.

The [Guardana](https://github.com/guardana/guardana) bridge stays optional and
off unless configured
([ADR-0024](docs/adr/0024-control-and-guardana-are-independent.md)).

## 1.0

1.0 commits to supported contracts and security claims. It requires published
compatibility and migration policies, a second implementation of the action
digest passing the same golden fixtures, two independently maintained
integrations, an external review of the threat model, an exercised disclosure
process, adversarial isolation tests, tested offline use and recovery, and
reproducible benchmarks of sustained tail latency. A claim of evidence
integrity needs hash links and signed checkpoints. A count of
features alone cannot meet this gate.
