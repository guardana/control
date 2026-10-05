# ADR-0039: Many channels into one core: what each may see, claim and do

Status: accepted
Date: 2026-10-03

Amended by [ADR-0040](0040-observations-a-record-a-log-and-one-importer.md):
in observation version 0.1 content is always dropped and a capture setting
refused, and a source descriptor is unsigned, so no finding stops a run on its
trust. Amended before its first release: the next milestone also holds procedures and
findings over both exports, as the roadmap's second milestone says; and a
correlation joined only by a trace id is no stronger than claimed, since the
agent supplies that id on both sides, so it cannot stop a run.

Amended by [ADR-0041](0041-coverage-per-declared-path.md): coverage is
reported by a command that reads the planes' configurations, the source
descriptors and their logs, not by a plane, and no plane reads what it prints.

Amends the order of [ADR-0026](0026-first-value-and-external-extension-paths.md)
and withdraws the in-process detector seam of
[ADR-0007](0007-repository-layout-and-dependency-rule.md). Builds on
[ADR-0035](0035-a-versioned-evidence-export-and-a-bounded-query.md) and
[ADR-0037](0037-an-evidence-consumer-in-a-module-of-its-own.md).

## Context

The project is meant to supervise an organization's agents: see what they do,
compare it with what they were meant to do, report what departs from it, and
stop an agent where the operator has said it may be stopped. The maintainer's
direction is that the Model Context Protocol (MCP) is one channel among
several, and that version one must let other channels plug into the same
core: an agent framework's hooks, the traces and logs an agent's runtime
writes, the access logs of the proxies its traffic passes. Most of the time
the plane should watch and report through the paths an operator configured;
it should intervene only where it is needed and allowed.

Much of the core already serves any channel. The action
envelope, the kernel, the pipeline's admit, close, abort and preview, and the
evidence event are protocol-neutral, and the MCP adapter is the one
enforcement point that uses them; the finding is a v1 contract nothing raises
yet. The evidence export
([ADR-0035](0035-a-versioned-evidence-export-and-a-bounded-query.md)) and a
consumer in a module of its own
([ADR-0037](0037-an-evidence-consumer-in-a-module-of-its-own.md)) already let
another program read what the plane decided.

An outside review found the order wrong in one place: the supervisor came
before any source but the plane's own events. A supervisor built only on what the MCP gateway saw cannot see an action
that went around the gateway, and nothing in it would say so.
Three facts constrain the fix:

- An evidence event cannot carry an observation. Every event names an
  enforcement mode, a chain refuses any request event before
  `ACTION_PROPOSED`, and a reader of export 1.0 refuses a line of a type it
  does not know.
- A finding cannot be raised by a detector outside the plane: the chain
  refuses one before `ACTION_PROPOSED`, and a finding names no tenant or
  project.
- Pause authority today is a file anyone who can write it may change, with
  global, provider and action scopes only.

## Decision

**Five ports, one vocabulary.** A channel is a way an agent's actions reach
Control, through an enforcement point or a sensor. Every channel and every
extension is one of five ports, and says which:

| Port | Sees | May do | Contract |
| --- | --- | --- | --- |
| Enforcement point | a proposed action before its effect | block, hold for approval, attach obligations | action envelope, decision and evidence event v1, as today |
| Sensor | an effect after it happened, or an indirect signal | report only | an observation and a source descriptor, new |
| Detector | bounded, cursor-ordered exports | raise a finding that cites its evidence | a finding record around finding v1, new |
| Reaction | findings and a signed route | add a scoped, expiring stop at enforcement points | a reaction source the plane reads, new |
| Notifier | findings | deliver them at least once, each with a stable id a receiver drops repeats by | a notifier route, new; no authority over a plane |

Notifying and stopping stay two types, so that a mistake in an alert route
can never stop anything.

**An observation is testimony, not evidence of enforcement.** Its record is a
sibling contract, `guardana.control.observe.v1alpha1`, never an action
envelope: it is never digested, never bound by an approval, and names no
mode, verdict or decision. It carries its source and that source's version,
a trust level taken from the source's descriptor and never from the data
(self-reported by the agent's own process, platform, or independent of the
agent; unspecified reads as self-reported), the time of the event (absent
means unknown, never the time it arrived) and the time it arrived, a stage
(proposed, started, completed, failed, or indirect), the version of any
outside convention it was read under (OpenTelemetry's GenAI conventions are
still in development, so a descriptor pins one), and correlation
identifiers with the basis of the correlation (none, claimed, joined to a
plane event, or bound to an authenticated run; unspecified reads as none).
Hidden model reasoning is dropped at every setting; prompts and outputs are
kept only where capture is turned on, with a redaction profile. An observation
names a tenant and a project, and goes to a log of its own, under the same
owner-only, retention and export rules as the evidence, read through an export
that follows ADR-0035's shape. The v1
contracts do not change.

**Coverage is stated per path, never as a percentage.** For each path an
operator declares (a tool, an API, a process, an egress destination) the
plane reports one of: enforced; decided but not enforced (a plane in
`OBSERVE`); observed, with the source's trust; inferred; not covered. A
standing row says that paths nobody declared are unknown. A source silent past
its heartbeat turns its paths unknown. A missing event is unknown, never
proof that nothing happened.

**Watch by default, intervene where configured.** Enforcement modes keep
their meaning, and a sensor has none. A finding carries an escalation level,
apart from any mode: inform (recorded), alert (sent by a notifier), review
(an alert awaiting acknowledgement), stop. A stop refuses a run's later calls
at the enforcement points that read the reaction source, for a scope and an
expiry; it never cuts a call already running, a process, or a path that does
not pass an enforcement point, and it never changes a plane's mode. Only a
deterministic finding marked confirmed may stop by default; a heuristic or
model finding stops nothing unless an explicit risk setting says otherwise. A
reaction route is signed under a key apart from the policy key, verified as a
bundle is, with a serial a plane never lowers. A stop names a run by the
identity the plane gave it, never by a run id a source reported, and a finding
whose correlation is only claimed cannot stop. A finding that rests on a gap
or on a source past its heartbeat is unknown, and an unknown finding stops
nothing. A reaction source the plane cannot read or verify blocks every call,
as an unreadable pause file does; an expired entry stops nothing. Run scope is the
only scope until operators authenticate; agent and principal scopes come with
it.

**Extensions are programs, definitions are documents.** An extension is a
separate process speaking a versioned data contract and declaring what it
can do; nothing is loaded into a plane's process. Procedures, detector rule
packs, source descriptors, notifier routes and reaction routes are versioned
documents, read strictly and tested with cases as policies are; the ones that
can stop something are signed. A port's conformance suite is a set of input
and expected output files any language can run, as the digest's golden files
are. The Go seams `pkg/adapter`, `pkg/detector` and `pkg/policyprovider` stay
reserved until two consumers use them, and ADR-0007's `detectors/builtin/`
is withdrawn: detectors run over exports, outside the plane.

**Order.** The supervisor's milestone becomes one task under supervision
across two channels: the MCP plane and one sensor of another kind (an agent
runtime's OpenTelemetry traces), a coverage map, the supervisor over both
exports, a local notifier and a run-scoped stop, before any public SDK. The
integrations milestone adds
an enforcement API for framework hooks, a second sensor independent of the
agent (an egress proxy's access log), and publishes the port contracts with
their conformance suites once two consumers have used them.

**Where the code goes.** Pure evaluation of procedures, rules and coverage
lives in `internal/supervise`, a guarded tree held to the dependency rule
like the kernel's; importers in `internal/ingest` and `adapters/<source>`;
notifiers in `adapters/`. Their commands read a configuration of their own,
not the plane's. One contract per package, and every file within the size
budget the gate holds.

## Security / compatibility impact

No language model enters the authorization path; a finding never grants, a
sensor never grants, and an observation is never an input to a decision. The
kernel, its guarded trees and the v1 contracts are unchanged, and `buf
breaking` keeps examining v1 while the observation package is unstable.

An automatic stop is a lever for denial of service: a false finding, or text
that steers a model detector, can stop a run. Hence the defaults above, the
run scope, the expiry and the signed route. A reaction source the plane
cannot read blocks every call on the planes that read it, which is the
fail-closed side, and is documented with it.

Self-reported telemetry is the agent's own account and can omit or forge
events; a finding that rests only on self-reported observations is at most
suspected. A proxy's log attributes a connection only as well as the proxy
identifies its caller. Content capture can record what people typed, so it
stays off by default and is scoped to a tenant and a project.

## Alternatives considered

- An observation as a new event kind in evidence v1: one stream, but it
  breaks every strict 1.0 reader, needs a mode it does not have, and mixes
  what the plane did with what a source says happened.
- A JSON Lines schema for observations and no typed contract: the cheapest,
  but a sensor in another language gets nothing to compile against; ADR-0035
  chose JSON for a wrapper, not for a record.
- The supervisor over the plane's events first, other channels later
  (ADR-0026's order): shows the MCP path well and a path around it not at all.
- Extensions loaded into the plane's process: they would share its authority
  and its failures; ADR-0007 and ADR-0026 already refuse it.
- A public SDK first: it would fix a surface before two consumers have shown
  what a stable one is.

## Consequences

The next milestone adds one sensor that is not MCP, a coverage map, one
notifier and a run-scoped stop. Every view the supervisor draws can say what
it did not see. Integrators get a contract to write against before a Go API
exists, and a detector or a notifier can be written in any language. The cost
is a second family of contracts to version, a reaction source with its own
key, and a guarded tree to keep pure.

## Validation

- `buf breaking` still examines `guardana/control/v1` once unstable packages
  are excluded, and a dependency listing shows nothing under the observation
  package reaching `internal/core` or `internal/canon`.
- Importer fixtures: a sampled source, an event with no time, an unknown run,
  a duplicate span, a skewed clock, a reasoning part (dropped and counted at
  every setting), content dropped unless captured.
- Coverage tests: a plane in `OBSERVE` never shows a path as enforced; a path
  seen only by a self-reported source shows as such; a silent source turns
  its paths unknown.
- The milestone 2 demonstration shows a path that went around the MCP plane
  as observed or not covered, never as enforced, and a stop that refuses the
  next call of one run and of no other.
