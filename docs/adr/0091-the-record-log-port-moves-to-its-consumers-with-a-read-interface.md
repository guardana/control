# ADR-0091: The record log port moves to its consumers, with a read interface

Status: proposed
Date: 2026-11-27

Amends [ADR-0072](0072-the-collector-keeps-bounded-segments-of-one-project-behind-a-credential.md)
on where the `RecordLog` port is declared and how it is read.

Amends [ADR-0066](0066-each-decision-about-authority-is-a-port.md) on the
note that `RecordLog` falls short of it.

## Context

ADR-0072 declared the `RecordLog` port beside its one kind, in
`internal/segmentlog`, and said it falls short of ADR-0066 in three ways: the
interface is declared beside the kind, not by its consumer; the kind is fixed in
code; it has no diagnostics key. It said the change that adds a second consumer
or kind moves the interface, adds a read interface and a kind selector, and
gives the port its bounds and diagnostics.

The evidence trail (`internal/trailfile`) writes through the port but reads
through the default kind's own read-only view: export, cursors and `trail
segments`. Its duplicate check reads segment files by name, around both the port
and the view.

The port hands out operating-system file handles: the live segment is opened
through a callback on an `os.Root`, and the view returns an `*os.File`. A second
kind could not implement it without files. The view answers whether a writer
holds the log as a bool and an error: three states folded into two values.

ADR-0092 puts the observation and finding logs on the same port, so it gets two
more consumers.

## Decision

- The port is declared in `internal/recordlog`, which imports no file-system
  types and not the kind. It has a write side, `Log` (append whole lines, seal,
  prune through a number, locate a segment by its key, the manifest, a read-back
  of lines it wrote by offset and digest, its statistics, close), and a read
  side, `Reader` (the manifest, locate, the first segment, the segment after a
  number, open a segment's content as a reader at offsets, whether a writer
  holds the log, its statistics).
- Whether a writer holds the log is a named state: `unknown` (the zero value),
  `held` or `free`.
- The kind is a typed value. Its zero value is refused by every opener, and
  `segments` is the only kind; no configuration key selects it until a second
  kind exists, and then a key with no default is added. The record says this is
  the shortfall that remains against ADR-0066.
- `internal/segmentlog` is the `segments` kind: it opens a `Log` and a `Reader`
  from the same configuration; opening the live segment through the directory it
  judged stays an option of that kind, outside the port.
- The duplicate check of the evidence trail is the write side's read-back, so no
  consumer reads segment files by name.
- The bounds are part of the configuration (segment bytes, log bytes, line
  bytes, segments in the manifest). The statistics name the kind, the sealed,
  pruned and dropped segments, the retained bytes, the bounds and how many
  appends the budget refused; `trail segments` prints them. The collector still
  has no `/metrics` of its own.
- The manifest's format `guardana.control.segments` `0.1` is unchanged for the
  evidence trail; ADR-0092 adds `0.2`.

## Security / compatibility impact

No file format, export, cursor or command line changes for the evidence trail;
what changes is where the port is declared and that every read goes through it.
A dependency test refuses an import of the kind, or of file-system types, from
the port package.

## Alternatives considered

- Moving the existing types as they are: the port would still hand out file
  handles.
- Waiting for a second kind: B14 needs the port's other consumers now, and the
  duplicate check would keep reading files around it.

## Consequences

A second kind, local or remote, implements two interfaces and brings no reader
of its own to the consumers. The trail's export and duplicate check change
implementation, so their tests run unchanged against the new code.

## Validation

The record-log conformance cases in `testdata/ports/record-log/` run against the
port, each with its negative control; the zero kind is refused; a holding state
of `unknown` is never read as `free`; every existing trail export, cursor and
duplicate test passes unchanged; `scripts/ports.txt` names the port's new
package; a dependency test holds the port package free of the kind and of
file-system types.
