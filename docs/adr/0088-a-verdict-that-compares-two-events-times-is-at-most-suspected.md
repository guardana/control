# ADR-0088: A verdict that compares two events' times is at most suspected

Status: proposed
Date: 2026-11-21

Amends [ADR-0045](0045-one-run-is-supervised-against-its-procedure.md) on the
deadline, [ADR-0047](0047-a-procedure-states-its-exceptions-resources-and-children.md)
on the retry rules and [ADR-0083](0083-a-finding-may-stop-an-agent-or-a-principal.md)
on the rules that may stop an agent or a principal.

## Context

`DEADLINE_EXCEEDED` measures the span between a run's first and last plane
events. `DENIED_ACTION_RETRIED_ARGUMENTS` and `DENIED_ACTION_RETRIED_RESOURCE`
order a retry after a denial. Both read each event's time as the plane stamped
it, and both were `CONFIRMED` when the two events came from one export, on the
premise that one export holds one plane's clock.

That premise does not hold. No event and no export line names its plane, a
run may span planes
([ADR-0082](0082-a-signed-document-names-its-plane-and-a-signed-plane-serves-one-tenant.md)),
and an export can hold events that two planes wrote. Two times read from one
export may come from two clocks, and nothing in the evidence says whether they
do. A confirmed finding is the bar for a stop, so a skewed clock could stop a
run, an agent or a principal that did nothing the rule names.

## Decision

- No verdict that orders or measures two events' times is `CONFIRMED`.
- `DEADLINE_EXCEEDED`, `DENIED_ACTION_RETRIED_ARGUMENTS` and
  `DENIED_ACTION_RETRIED_RESOURCE` move to rule version `2`. Their row in the
  rule table may not confirm and may not stop, so each finding is `SUSPECTED`
  at most, whatever exports its events came from. The rule's version is part
  of the finding id, so the same evidence gives a new id.
- None of the three is a stopping rule at either version. `route sign`,
  `react` and a plane loading its route refuse a route that names one, and the
  refusal names the rule.
- The rules that may stop a run are `REPEATED_DENIAL` version `2`,
  `STEP_OUTSIDE_PROCEDURE` and `RESOURCE_OUTSIDE_RUN`. Only `REPEATED_DENIAL`
  may stop an agent or a principal.
- A stop already written under version `1` of the three stays on its list: an
  upgrade lifts nothing. `stops init --carry-route` may name a route that
  names version `1`, and the carried list keeps those stops while the new
  route names the stop's procedure, its scope is one the old rule allowed and
  it lasts at most the stop lifetime bound. No writer appends a new stop of
  those versions. The operator finds them with `stops list` and lifts them.
- `DENIED_ACTION_RETRIED_AROUND` is unchanged: `SUSPECTED` at most, and
  `INDETERMINATE` while no importer stamps a call's start.

## Security / compatibility impact

A stop needs fewer kinds of evidence: a retry or an overrun is reported but
stops nothing. A plane started with a route that names one of the three fails
at start until the route is signed again without it. Findings of the three
rules change id and verdict, so a notifier delivers them again as new records.
No reason code, wire contract or digest changes.

## Alternatives considered

- Keep `CONFIRMED` within one export: rests on the premise this record
  refutes.
- Bound the skew between planes and confirm beyond it: no plane publishes its
  clock's error, and a bound chosen here would be a guess.
- Retire the three rules: the reports still help an operator; only the stop
  rests on a time comparison.

## Consequences

An operator who relied on a retry or deadline stop has none; `REPEATED_DENIAL`
remains the backstop for repeated denied calls. Each operator with such a
route signs it again and carries the list. The way back is a plane identity
on every event and export line, under a later contract version, with which a
verdict on two events of one plane's clock could confirm again; that is
backlog, not decided here.

## Validation

`internal/supervise` gives `SUSPECTED` for a deadline and a retry inside one
export, and its rule table lets none of the three confirm or stop.
`internal/reaction` refuses the three in a route at both versions and keeps a
carried stop of version `1` under the stated bounds. `route sign`, `react` and
the plane's route load each refuse a route naming
`DENIED_ACTION_RETRIED_ARGUMENTS` `1` or `DEADLINE_EXCEEDED` `1` by name, and
`react` stops nothing for a confirmed version `1` retry already in a findings
log.
