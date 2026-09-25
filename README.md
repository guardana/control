# Guardana Control

An inline control and evidence layer for AI agents. It decides whether a
proposed tool call may happen, enforces that decision, and records what was
decided and why.

## Status: alpha

An `experimental` gateway decides and enforces the tool calls an agent makes
over the Model Context Protocol (MCP). Do not deploy this as a security
boundary. The longer-term goal is to supervise an organization's agents:
[ROADMAP.md](ROADMAP.md) describes it and [docs/status.md](docs/status.md) lists
what exists. To try it, follow [the tutorial](docs/get-started/try-the-demo.md).

## The problem

An agent chooses its tool and its arguments at run time, from text it read
moments earlier. The tool holds the credential and executes whatever arrives
at its API. In most deployments nothing between the two asks whether this
agent, acting for this person, may do this to this resource now. When the
action turns out wrong, the record is a transcript and a log, neither designed
as evidence. Every new tool widens that gap.

## What it does

It intercepts a consequential action before it happens and returns one of five
verdicts:

| Verdict | Meaning |
| --- | --- |
| `ALLOW` | The action proceeds. |
| `DENY` | The action is blocked. |
| `REQUIRE_APPROVAL` | A person approves this exact action, or it does not run. |
| `ALLOW_WITH_OBLIGATIONS` | The action proceeds under conditions that are enforced. |
| `INDETERMINATE` | The decision could not be made. It is never an allow. |

It enforces the verdict except in `OBSERVE`, and appends an evidence record
unless the operator let a read run unrecorded. The record says who acted, on
whose behalf, on what, what was decided, which policy version decided it, and
how the call ended, with a hash of the result rather than its content.
Precedence between the verdicts and the fail-closed rules is in
[ADR-0012](docs/adr/0012-policy-kernel-semantics.md), the evidence and privacy
defaults in [ADR-0004](docs/adr/0004-evidence-and-privacy-defaults.md).

## Architecture in 30 seconds

Today the enforcement point sits between an agent and its MCP servers and
consults the built-in policy engine. An external decision point (PDP) can veto
over AuthZEN but cannot grant (`experimental`).

```mermaid
flowchart LR
    AG[Agent] -->|proposed action| PEP[Enforcement point]
    PEP <--> POL[Built-in policy engine]
    PEP -.->|can veto| PDP[External PDP]
    PEP -->|require approval| APR[Approval provider]
    APR --> PEP
    PEP -->|allow| TOOL[Tool or API]
    TOOL -->|result| PEP
    PEP --> EV[(Append-only evidence)]
    EV --> OTEL[OpenTelemetry export]
```

The enforcement point is the only component this project adds to the request
path. Evidence goes to a local spool before export, so decisions do not wait
for the exporter. If the spool fills, the plane counts what it drops.

## Where it is going

Everything beyond that path is `planned` and follows [ROADMAP.md](ROADMAP.md).
A supervisor compares what agents do with their procedures and permissions,
reports to the operator's alerting and logging, and can stop an agent.

```mermaid
flowchart LR
    subgraph IN[Inputs]
        PX["Proxy: MCP today, HTTP tool APIs next"]
        PT[Framework ports]
        FD["Feeds: agent traces, logs, process events"]
    end
    subgraph CTL[Guardana Control]
        DK["Decision kernel: policy, approvals, stop"]
        SV["Supervisor: procedures, deviations, access attempts, unfinished work"]
        EV[(Evidence)]
    end
    subgraph OUT[Outputs]
        AL["Alerts: webhooks, chat"]
        EX["OpenTelemetry, metrics, SIEM"]
        GR[Run graph and console]
    end
    PX --> DK
    PT --> DK
    FD --> SV
    DK --> EV
    EV --> SV
    SV --> AL
    EV --> EX
    SV --> GR
    GR -->|stop an agent| DK
```

Rules and scenarios are already documents a team writes and tests; procedures
will be too. Adapters, detectors and policy providers are the planned seams for
extensions ([docs/extending/](docs/extending/adapters.md)).

## Install

Each release has archives for Linux and macOS on amd64 and arm64, holding both
binaries, `guardana-gateway` and `guardana-control`. Download one from the
[releases page](https://github.com/guardana/control/releases), check it against
the signed `checksums.txt` as [RELEASING.md](RELEASING.md) shows, and put the
binaries on your `PATH`.

With Go 1.27.1 or later, build them from source instead; such a binary reports
its version as `dev`:

```bash
go install github.com/guardana/control/cmd/guardana-gateway@latest
go install github.com/guardana/control/cmd/guardana-control@latest
```

## Related project: Guardana

[Guardana](https://github.com/guardana/guardana) is a separate open-source
project that verifies AI systems before and after deployment: it scans
artifacts, probes endpoints and reads recorded traces, outside the request
path. Guardana Control decides each call inside it.

The two are independent: neither needs the other to build, run or be useful. They can work together through the formats each
one publishes; a bridge between them would be an optional module
([ADR-0024](docs/adr/0024-control-and-guardana-are-independent.md)).

## Links

- [Documentation index](docs/index.md)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md)
- [Governance](GOVERNANCE.md)
- [Roadmap](ROADMAP.md)
- [Releases and how to verify one](RELEASING.md)
- [Architecture decision records](docs/adr/README.md)
- [Code of conduct](CODE_OF_CONDUCT.md)
- [Trademarks](TRADEMARKS.md)
- [License](LICENSE), Apache-2.0
