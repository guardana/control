# ADR-0092: The observation and finding logs are segment logs of one project

Status: proposed
Date: 2026-11-27

Amends [ADR-0040](0040-observations-a-record-a-log-and-one-importer.md) on how
observations are stored and [ADR-0078](0078-a-restore-only-raises.md) on what a
backup holds. Builds on
[ADR-0072](0072-the-collector-keeps-bounded-segments-of-one-project-behind-a-credential.md)
and [ADR-0091](0091-the-record-log-port-moves-to-its-consumers-with-a-read-interface.md).

## Context

`observe import` appends to one file, `observations.jsonl`, and `supervise` to
one file, `findings.jsonl`. Each is bounded at 1 GiB, after which writes are
refused and the operator rotates the file by hand; no segment is sealed or
pruned, an export cursor never expires, and rotating the file is the only way
to drop old records.

One observation file can hold the sources of several tenants and projects.
The findings file starts with a header naming a random `log_id`; an export
cursor of one log is never read as a position in another because of it. Finding
ids are deterministic, so a log written again can start with the same first
line.

The evidence trail already keeps bounded segments of one project with a
manifest, pruned only by an operator (ADR-0072), and its export carries
`pruned_before`.

Neither log is in `state backup`, and none of the three logs is a configuration
key `state backup` could name.

## Decision

- Each log is a directory of segments under the record log port of ADR-0091,
  bound to one tenant and one project. `observe import` binds an observation log
  to its source descriptor's tenant and project and refuses a source of another.
  `supervise` binds the findings log to the project it supervises.
- The manifest goes to `guardana.control.segments` `0.2`, which adds a required
  `log_id`, written when the log is made. The evidence trail keeps `0.1` and is
  never converted. A `0.2` manifest is read only in its one spelling.
- The bound refuses and nothing prunes on its own: an append past the log's byte
  bound is refused, and the operator prunes sealed segments with
  `guardana-gateway prune`, the command that prunes a trail directory today.
- The observation export and the findings export go to format `0.2`: the header
  names `pruned_before`, a cursor names its segment by a key that folds in the
  log's `log_id`, and a cursor of a pruned segment is refused as expired. A
  cursor of another log is refused as another file, never read as another
  position. `coverage` and `supervise` refuse an export as whole while
  `pruned_before` is above zero, as coverage does for the trail.
- A resend after its segment was pruned is written again, as in the trail; the
  duplicate index covers the segments not pruned.
- The single-file form is still read and exported, never written: `observe
  import` and `supervise` refuse to append to it and name the directory option
  that replaces it.
- `guardana-gateway log backup <dir> <dest>` copies a log of any of the three
  kinds: it is refused while a writer holds the log and takes no lock file of
  its own; it copies the sealed segments, the live segment up to its last whole
  line and the manifest. `guardana-gateway log restore <src> <dir>` writes only
  into a directory that does not exist, checks the copy by opening it read-only,
  and then renames it into place. The logs stay outside `state backup`.

## Security / compatibility impact

A reader of a `0.1` export that refuses an unknown member fails loudly on a
`0.2` export, never misreads it. An existing observation or findings file can
no longer be appended to; the first import or supervise after the upgrade needs
a directory, and a record already in the old file is not deduplicated against
the new log, so consumers deduplicate by id across the two.

A restored log loses what came after its backup, and nothing in the export
marks that gap; a cursor taken after the backup is refused rather than read as
another position.

At the bound, imports and supervision stop writing until an operator prunes,
which errs on keeping evidence.

## Alternatives considered

- Keeping one file per log with a larger bound (the bound still ends in a
  manual rotation and a cursor that never expires).
- One observation log for every project (the port binds a log to one project,
  and the export could not say whose records it lacks).
- Adding the logs to `state backup` (it would tie a plane's configuration to
  directories other commands own).
- Pruning the oldest segment automatically at the bound (it would drop
  evidence nobody chose to drop).

## Consequences

The observation and finding logs gain rotation, a budget, cursor expiry and
an operator's prune, with the same commands as the trail; their consumers
update to export `0.2`.

## Validation

For observations and findings, as for the evidence trail: a seal at the
segment bound, a refusal at the log bound, a prune by an operator, a cursor of
a pruned segment refused as expired, an export cut at every record and resumed
that writes every record once, a duplicate across a seal and a reopen written
once, a cursor of another log refused; `log backup` refused while a writer holds
the log; a restore only into an absent directory; a cursor taken after a backup
refused after its restore; the single file read and exported, and refused for
appends; the inspection purity tests hold the new readers and `log backup`.
