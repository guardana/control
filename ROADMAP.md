# Roadmap

What has to become true, in the order it is planned, without dates.

Guardana Control is meant to supervise an organization's AI agents: show what
they do and what data they reach, check them against policy and their
procedures, report deviations, access attempts and unfinished work, draw each
run as a graph, and let an operator with the right permission stop an agent.
Blocking a call inline is how it stops one; the rest runs beside the agents.

Everything here is `planned`, and a finished item is deleted.
[docs/status.md](docs/status.md) is the inventory of what exists.

## Procedures an agent is held to

Done when a procedure can be written down as a versioned document: the steps a
task takes, their order, the steps without which it is not finished, the
exceptions it allows and its deadline. A run names the procedure it follows and
has an identity of its own, so two runs of one agent never share state.

## A supervisor beside the agents

Done when a supervisor reads each run's evidence and reports steps outside the
procedure or out of order, skipped required steps, and runs unfinished past
their deadline. It also reports a run that carries on after a failed step, and
an attempt to reach a tool, resource or permission the run does not hold,
including a denied action retried in another form. Later checks cover loops,
fan-out, paths across tenants, empty results taken as success, and drift in a
tool or a permission. Each report cites its evidence and each kind has positive,
negative and indeterminate fixtures. When evidence is missing or the
supervisor falls behind, it says so. It adds no latency to a decision and
changes no verdict.

## Every run as a graph

Done when one run can be read as a graph of the expected and the observed
steps, the data and authority each step used, the reports raised, and the
policy version behind each decision, without joining identifiers by hand.
Missing evidence shows as a gap, not as nothing.

## Stopping one agent

Done when an operator with the right permission can stop one run, one agent or
one principal rather than a whole upstream, a severe report can stop a run
where the operator configured that, and shadow mode decides a candidate policy
beside the enforced one and records both.

## Reach across frameworks and agents

Done when:

- a framework reaches the plane through one language-neutral API over the
  published contract; a port translates the framework's hook into an envelope,
  enforces the answer and never decides;
- ports exist for LangGraph and the OpenAI Agents SDK first, with Python and
  TypeScript clients, then for what adopters ask for, each passing one
  conformance suite and stating which tool paths it covers;
- a stack without a port can go through the project's own proxy: MCP today, a
  generic one for HTTP tool APIs next;
- an agent-to-agent (A2A) gateway binds authority across a handoff, delegation
  is task-bound and expires, and workload identity comes from the platform.

## Quality of outcomes

Done when a run is judged against its procedure as finished, finished with
steps skipped, degraded or unknown. Approve, reject and edit outcomes are
recorded with the cost per accepted outcome, and reports show how these change
after a model, prompt or tool changes. A quality judgment never influences a
verdict.

## A fleet in one view

Done when many planes report to one control plane that holds the inventory of
agents, tools and procedures, distributes signed policy, authenticates its
users and keeps tenants apart, and the graph and reports cover the fleet.

## Decision semantics, proven in isolation

Done when a second implementation of the action digest, in another language,
passes the same golden fixtures; a bundle's expiry is signed and its serial
survives a restart; and every fixture produces a complete evidence record
naming the policy version behind it.

## An optional bridge to Guardana

Done when someone who runs both this project and
[Guardana](https://github.com/guardana/guardana) can connect them: the plane's
evidence exported in a form Guardana reads, and a Guardana contract turned into
a signed policy. It is an optional adapter, off unless configured; nothing else
waits on it, and this project loses nothing without it
([ADR-0024](docs/adr/0024-control-and-guardana-are-independent.md)).

## 1.0

1.0 is a compatibility and security commitment, not a feature count. Done when:

- the public Go surface (`pkg/contract`, `pkg/adapter`, `pkg/detector`,
  `pkg/policyprovider`) is stable, the schema compatibility policy is
  published, and migration tooling exists for both;
- the control plane runs highly available, with backup, restore and retention
  tested rather than documented;
- policy distribution is signed, and an air-gapped deployment is tested, not
  described;
- multi-tenant isolation has its own adversarial tests;
- a sustained benchmark is published with a method anyone can rerun,
  reporting tail latency;
- the threat model has been refreshed by someone outside the project;
- the disclosure process has been exercised at least once, end to end;
- at least two third-party integrations are maintained outside the project,
  the only real proof that the public Go surface works.
