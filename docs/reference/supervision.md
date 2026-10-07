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
([ADR-0045](../adr/0045-one-run-is-supervised-against-its-procedure.md));
`react` turns a confirmed finding into a stop of its run, which a plane with
a route reads ([reaction.md](reaction.md)).

```
guardana-control supervise --procedure <file> --runs <dir> --run <run-id> --findings <dir>
    [--evidence <export>]... [--source <descriptor> --log <dir>]...
guardana-control notify --findings <dir> --state <dir> [--init] [--timeout <duration>]
    -- <program> [<arg>]...
```

## The procedure

A strict JSON document of at most 64 KiB: an unknown or repeated member is
refused, and so is a cycle in the order or a step it does not know. Its digest is recorded with
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
run id, tenant and project. Only a tool call is a step: a prompt or a resource
read through the plane never counts as one. An observation joined to a plane
event by trace and span id is that call, not a second one. Everything else is counted in the
report and left out.

An evidence export is read only when it is a regular file of this account
that the group and others cannot write, as the procedure and the descriptors
are. A `CONFIRMED` finding rests on the export being the plane's, and
nothing but that file's owner and mode protects it.

## The rules

| Rule | Fires when |
| --- | --- |
| `REPEATED_DENIAL` | one tool on one upstream is denied by policy `max_denials` times; a block of the plane's own (a pause, an unclassified tool, a spent or expired approval) is counted, not a denial |
| `STEP_OUTSIDE_PROCEDURE` | a tool neither a step nor allowed is called, or reported and not joined to a plane call |
| `DEADLINE_EXCEEDED` | the run's events span more than `deadline_seconds` |
| `REQUIRED_STEP_SKIPPED` | a required step has no instance |
| `STEP_OUT_OF_ORDER` | a step comes before a step it must follow; an order that cannot be told is indeterminate, never a pass |
| `CONTINUED_AFTER_FAILURE` | another step is proposed after a step's failure, before any retry of it; the first instance of a step found out of order is that finding, not this one; an order that cannot be told makes it indeterminate |

An order cannot be told when a proposal or a failure has no time, or between
two exports' proposals of one instant; within one export the plane's append
order holds. A retry continues nothing, though each denial of it counts
toward `REPEATED_DENIAL`, and an approval belongs to the step it held.

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

A record is schema `0.1` or `0.2`, and it is `0.2` exactly when it carries a
member `0.1` lacks
([ADR-0047](../adr/0047-a-procedure-states-its-exceptions-resources-and-children.md)):

| Member | In `0.2` |
| --- | --- |
| `EventRef.run_id` | the run of each plane event a finding rests on, on every event of the record |
| `FindingRecord.refs_left_out` | references cut past ten, doubtful ones kept; the verdict weighed all |
| `SuperviseReport.children` | `CHILDREN_MODE_INHERIT` (the root's whole tree judged) or `CHILDREN_MODE_SEPARATE` (the run's own calls only) |
| `SuperviseReport.run_tree` | each member's run id and parent: the supervised run first, every other run after its parent; under separate, the children not judged |
| `LogHeader` | the first line of a log this release creates: `log_id` is `log-` and 32 hex digits of 128 random bits |

A log made before this release has no header, keeps none and is read as it
is; an empty one gains a header when opened. A torn header is cut like any
torn line. The
reader refuses an unknown schema or a member its schema lacks, naming the
field; a 0.9 reader refuses every `0.2` line, by the field it does not know.

## notify

`notify` hands each `alert` record, in log order and as one line on its
standard input, to the program after `--`, without a shell, and kills its
process group at the timeout (30 s by default) and again once the program
has exited, so no child it started outlives the delivery. A delivery is keyed on the
finding id and verdict and marked in the state only after the program exits
0, so a crash delivers that one record again: a receiver drops a key it has
seen. The state is an owner-only directory, locked while a run holds it;
`--init` starts one where none is and is refused over one. A failed delivery
is retried by the next run. Nothing replays a delivered record but a new
state, which delivers every alert again. Exit 0 when nothing failed; 1 when a delivery
failed, on an interrupt, or when the run stopped after a program had run (a
mark that did not reach the disk, a log that changed under it); 2, printing
nothing, when the state or the log is refused before any program ran.

## findings export

```
guardana-control findings export --findings <dir> [--after <cursor>] [--limit <n>]
    [--max-bytes <n>]
```

`findings export` writes the log as JSON Lines in the shape of the evidence
export, format `guardana.control.findings-export` version `0.1`, unstable as
the records are
([ADR-0048](../adr/0048-one-run-drawn-as-a-page-and-findings-exported-with-a-cursor.md)):
`header`, then `finding_record` and `supervise_report` each carrying its log
line, `gap`, `duplicate` and `trailer`. The log is judged as `notify` reads it,
under no lock, and exported only to the last write a report closes; a write
still open is the trailer's `tail_bytes`, and a gap when no writer holds the
log. A cursor holds in its own log only: by the header's `log_id`, or by the
first line of a log with no header. The trailer's `identity` says which:
`log_id`, `content` or `none`. Exit 0, 1 for a whole export with a gap, 2
refused or cut short.
