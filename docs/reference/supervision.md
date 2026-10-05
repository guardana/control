---
title: Supervision
summary: The procedure document, what guardana-control supervise checks for one opened run, the finding record and its log, and how notify delivers findings.
type: reference
covers: [internal/supervise/**, internal/findinglog/**, internal/notify/**, cmd/guardana-control/supervise*.go, cmd/guardana-control/notify.go, api/proto/guardana/control/finding/**]
---

# Supervision

`guardana-control supervise` checks one run an operator opened against the
procedure it was meant to follow, from the plane's evidence and what the
agent's runtime reported, and appends findings to a findings log.
`guardana-control notify` hands each alert in that log to a program of the
operator's, at least once. Both are `experimental`. Neither decides a call,
and no plane reads what they write
([ADR-0045](../adr/0045-one-run-is-supervised-against-its-procedure.md)).

```
guardana-control supervise --procedure <file> --runs <dir> --run <run-id> --findings <dir>
    [--evidence <export>]... [--source <descriptor> --log <dir>]...
guardana-control notify --findings <dir> --state <dir> [--init] [--timeout <duration>]
    -- <program> [<arg>]...
```

## The procedure

A strict JSON document: an unknown or repeated member is refused, and so is a
cycle in the order or a step it does not know. Its digest is recorded with
every finding; a procedure id and version the findings log has seen under
another digest is refused.

| Member | Meaning |
| --- | --- |
| `schema_version` | `0.1` |
| `procedure_id`, `version` | what the findings name |
| `steps` | each an `id`, the `tool` and `upstream` a plane sees, the names a runtime reports it under (`observed_as`), and `required` |
| `order` | each step id and the steps it must follow |
| `allow` | tools the run may also call, each with `tool`, `upstream` and `observed_as` |
| `rules` | for each of the six rules, a `severity` (`info` to `critical`) and an `escalation` (`inform` or `alert`) |
| `max_denials`, `deadline_seconds` | at least 1 each; leaving one out turns its rule off, and the report says so |

## What belongs to the run

Only a run opened in a runs directory is supervised; a plane's local run and a
run's children are refused. Plane events count when their run id and tenant
are the run's. Observations count when their correlation is claimed, with that
run id, tenant and project. An observation joined to a plane event by trace
and span id is that call, not a second one. Everything else is counted in the
report and left out.

An evidence export is read only when it is a regular file of this account
that the group and others cannot write, as the procedure and the descriptors
are. A `CONFIRMED` finding rests on the export being the plane's, and today
nothing but that file's owner and mode protects it.

## The rules

| Rule | Fires when |
| --- | --- |
| `REPEATED_DENIAL` | one tool is denied by policy `max_denials` times; a block of the plane's own (a pause, an unclassified tool, a spent or expired approval) is counted, not a denial |
| `STEP_OUTSIDE_PROCEDURE` | a tool neither a step nor allowed is called, or reported and not joined to a plane call |
| `DEADLINE_EXCEEDED` | the run's events span more than `deadline_seconds` |
| `REQUIRED_STEP_SKIPPED` | a required step has no instance |
| `STEP_OUT_OF_ORDER` | a step comes before a step it must follow; an order that cannot be told for want of a time is indeterminate, never a pass |
| `CONTINUED_AFTER_FAILURE` | another step is proposed after a step's failure; a proposal or a failure whose time is not known makes it indeterminate |

A retry of the same step is never a finding, and an approval belongs to the
step it held.

## The verdict

The weakest of what a finding rests on. Plane events in an unbroken chain can
make it `CONFIRMED`; an observation makes it `SUSPECTED`; a record in doubt (a
conflicting id, a broken chain, a source silent past its heartbeat) makes it
`INDETERMINATE`. The last three rules rest on something not seen: they are
`SUSPECTED` at most, `INDETERMINATE` while a `--source` given is silent,
never heard or has no descriptor at its path, and checked only on a closed
run whose exports are whole; otherwise the report says not checked and why.

## Output and exit status

One line per step with the requests matched and, apart, the observations of
it no plane call joins, which are reported by the runtime only and never an
instance of the step; then one line per finding and per rule, then
`not checked: source <id> absent`, `never heard` or `silent` for each
`--source` the run could not be judged against: a descriptor not at its path
(named by the path), a source with no import report, or one whose heartbeat
ran out before the run's last plane event.

| Exit | When |
| --- | --- |
| 0 | every rule checked or turned off, no finding, at least one plane event of the run read, and every `--source` read and heard within its heartbeat |
| 1 | a finding, a rule not checked, no event of the run, or a `--source` absent, never heard or silent |
| 2 | an input refused, or a procedure edited under its version: nothing on standard output and the log not opened; or a log that could not be opened or written: nothing on standard output, and a write cut short is one no reader takes |

The log is opened only after every input is accepted. Opening it judges its
directory and creates `findings.jsonl` empty when it is absent, even when no
event of the run was read and so nothing is written. A failure after the
write is committed, closing the log or writing the output, also exits 2, and
the write is kept.

## The finding record and its log

`guardana.control.finding.v1alpha1`, outside the v1 promise. A
`FindingRecord` holds the tenant, project, procedure (id, version, digest),
escalation, typed references (a plane event by event and request id, an
observation by source and observation id) and a v1 finding with its id, rule,
severity, verdict and run. Its id is `fnd-` and 32 hex digits, a function of
the run, the procedure, the rule and what it is about, so running the command
again finds the same ids. The log is one JSON Lines file, `findings.jsonl`, in
an owner-only directory; each run of the command ends its write with a
`SuperviseReport`, and a write without one is never read.

## notify

`notify` hands each `alert` record, in log order and as one line on its
standard input, to the program after `--`, without a shell, and kills its
process group at the timeout (30 s by default) and again once the program
has exited, so no child it started outlives the delivery. A delivery is keyed on the
finding id and verdict and marked in the state only after the program exits
0, so a crash delivers that one record again: a receiver drops a key it has
seen. The state is an owner-only directory, locked while a run holds it;
`--init` starts one, which delivers everything again. A failed delivery is
retried by the next run. Exit 0 when nothing failed; 1 when a delivery
failed, or when the run stopped after a program had run (a mark that did not
reach the disk, an interrupt, a log that changed under it); 2, printing
nothing, when the state or the log is refused before any program ran.
